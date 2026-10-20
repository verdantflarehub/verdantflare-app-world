package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/verdantflarehub/verdantflare-app-world/internal/artifactclient"
	"github.com/verdantflarehub/verdantflare-app-world/internal/buildinfo"
	"github.com/verdantflarehub/verdantflare-app-world/internal/coordination"
	"github.com/verdantflarehub/verdantflare-app-world/internal/dashboard"
	"github.com/verdantflarehub/verdantflare-app-world/internal/mcpregistration"
	"github.com/verdantflarehub/verdantflare-app-world/internal/world"
	"github.com/verdantflarehub/verdantflare-app-world/migrations"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "World startup or runtime failed; check controlled configuration and schema state")
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string) error {
	if len(args) != 1 || (args[0] != "serve" && args[0] != "migrate-fresh" && args[0] != "activate-fresh" && args[0] != "migrate-gates") {
		return errors.New("usage: world serve|migrate-fresh|activate-fresh|migrate-gates")
	}
	if os.Getenv("WORLD_DATABASE_URL") == "" {
		return errors.New("database required")
	}
	wf, err := strconv.ParseInt(os.Getenv("WORLD_COORD_FENCE"), 10, 64)
	if err != nil || wf < 1 {
		return coordination.ErrFenced
	}
	pf, err := strconv.ParseInt(os.Getenv("PROJECT_COORD_FENCE"), 10, 64)
	if err != nil || pf < 1 {
		return coordination.ErrFenced
	}
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	db, err := pgxpool.New(startup, os.Getenv("WORLD_DATABASE_URL"))
	if err != nil {
		return err
	}
	defer db.Close()
	if err = db.Ping(startup); err != nil {
		return err
	}
	if args[0] == "migrate-fresh" {
		return migrations.ApplyFresh(startup, db, pf, wf)
	}
	if args[0] == "activate-fresh" {
		return migrations.ActivateFresh(startup, db)
	}
	if args[0] == "migrate-gates" {
		return migrations.UpgradeGates(startup, db)
	}
	if err = migrations.CheckRuntime(startup, db); err != nil {
		return err
	}
	serviceToken := os.Getenv("WORLD_SERVICE_TOKEN")
	worldCoord, projectCoord := os.Getenv("WORLD_COORD_TOKEN"), os.Getenv("PROJECT_COORD_TOKEN")
	seen := map[string]bool{}
	for _, token := range []string{serviceToken, worldCoord, projectCoord, os.Getenv("PROJECT_SERVICE_TOKEN"), os.Getenv("ARTIFACT_SERVICE_TOKEN")} {
		if len(token) < 32 || strings.ContainsAny(token, " \r\n\t") || seen[token] {
			return errors.New("independent credentials required")
		}
		seen[token] = true
	}
	analysisConfig, err := readAnalysisConfig(os.Getenv, seen)
	if err != nil {
		return err
	}
	content, err := artifactclient.NewWithCA(os.Getenv("ARTIFACT_URL"), os.Getenv("ARTIFACT_SERVICE_TOKEN"), os.Getenv("ARTIFACT_CA_FILE"))
	if err != nil {
		return err
	}
	projects, err := world.NewProjectClientWithCA(os.Getenv("PROJECT_SERVICE_URL"), os.Getenv("PROJECT_SERVICE_TOKEN"), os.Getenv("PROJECT_CA_FILE"))
	if err != nil {
		return err
	}
	peer, err := coordination.NewClientWithCA(os.Getenv("PROJECT_SERVICE_URL"), worldCoord, os.Getenv("PROJECT_CA_FILE"))
	if err != nil {
		return err
	}
	svc, err := world.NewService(startup, db, content, projects, wf, map[string]coordination.Peer{"project": peer})
	if err != nil {
		return err
	}
	engine := svc.Coordination()
	if err = engine.Store.Check(startup, map[string]int64{"project": pf, "world": wf}); err != nil {
		return err
	}
	internal, err := svc.HTTPHandler(serviceToken)
	if err != nil {
		return err
	}
	mcp, err := svc.MCPHandler(serviceToken)
	if err != nil {
		return err
	}
	coord, err := engine.Handler(map[string]string{"project": projectCoord})
	if err != nil {
		return err
	}
	dir := os.Getenv("WORLD_DASHBOARD_DIR")
	if dir == "" {
		dir = "/app/dashboard"
	}
	assets, err := dashboard.Load(dir, buildinfo.Version)
	if err != nil {
		return err
	}
	assetHandler, err := assets.Handler(serviceToken)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.Handle("/internal/v1/coordination/", coord)
	mux.Handle("/internal/v1/", internal)
	mux.Handle("/api/v1/world/", internal)
	mux.Handle("/internal/dashboard/", assetHandler)
	health := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if db.Ping(c) != nil {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(204)
	})
	mux.Handle("GET /healthz", health)
	addr := os.Getenv("WORLD_LISTEN")
	if addr == "" {
		addr = "127.0.0.1:8092"
	}
	cert, key := os.Getenv("WORLD_TLS_CERT"), os.Getenv("WORLD_TLS_KEY")
	if (cert == "") != (key == "") {
		return errors.New("both TLS files required")
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if cert == "" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("non-loopback listener requires TLS")
	}
	mcpAddr, advertise, registryEndpoints := os.Getenv("WORLD_MCP_LISTEN"), os.Getenv("WORLD_MCP_ADVERTISE_URL"), os.Getenv("ETCD_ENDPOINTS")
	if err = mcpregistration.ValidateListener(addr, mcpAddr, advertise, registryEndpoints, cert != ""); err != nil {
		return err
	}
	server := &http.Server{Addr: addr, Handler: mcpregistration.BusinessHandler(mux), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 3 * time.Minute, IdleTimeout: time.Minute, MaxHeaderBytes: 16 << 10}
	if cert != "" {
		pair, loadErr := tls.LoadX509KeyPair(cert, key)
		if loadErr != nil {
			return errors.New("invalid listener TLS configuration")
		}
		server.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return errors.New("listener unavailable")
	}
	defer listener.Close()
	done := make(chan error, 2)
	serve := func(server *http.Server, listener net.Listener) {
		if cert != "" {
			done <- server.ServeTLS(listener, "", "")
		} else {
			done <- server.Serve(listener)
		}
	}
	go serve(server, listener)
	defer server.Close()
	var protocolServer *http.Server
	if mcpAddr != "" {
		protocolListener, listenErr := net.Listen("tcp", mcpAddr)
		if listenErr != nil {
			return errors.New("MCP listener unavailable")
		}
		defer protocolListener.Close()
		protocolServer = &http.Server{Addr: mcpAddr, Handler: mcpregistration.MCPHandler(mcp, health), TLSConfig: server.TLSConfig, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 3 * time.Minute, IdleTimeout: time.Minute, MaxHeaderBytes: 16 << 10}
		go serve(protocolServer, protocolListener)
		defer protocolServer.Close()
	}
	tools := []mcpregistration.ToolDefinition{}
	for _, tool := range world.MCPTools() {
		tools = append(tools, mcpregistration.ToolDefinition{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema})
	}
	publication, err := mcpregistration.Start(ctx, advertise, registryEndpoints, buildinfo.Version, tools)
	if err != nil {
		return err
	}
	if publication != nil {
		defer publication.Close()
	}
	worker, cancelWorker := context.WithCancel(ctx)
	defer cancelWorker()
	go svc.RunRecovery(worker)
	stopAnalysis := startAnalysisWorker(worker, db, svc, analysisConfig)
	defer stopAnalysis()
	select {
	case err = <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.New("World HTTP server failed")
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err = server.Shutdown(shutdown)
		if protocolServer != nil {
			err = errors.Join(err, protocolServer.Shutdown(shutdown))
		}
		return err
	}
}
