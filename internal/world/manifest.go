// Package project owns the portable engineering manifest and its semantic rules.
// Authorization and Artifact existence checks belong to the project service.
package world

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const MaxManifestBytes = 4 << 20
const MaxFiles = 10000

var idPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var tokenPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
var extensionPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*\.[a-z][a-z0-9_.-]*$`)
var devicePattern = regexp.MustCompile(`(?i)^(CON|PRN|AUX|NUL|COM[1-9¹²³]|LPT[1-9¹²³])(?:\.|$)`)

type ContentRef struct {
	StoreID    string `json:"store_id"`
	ArtifactID string `json:"artifact_id"`
	VersionID  string `json:"version_id"`
}
type File struct {
	ID      string     `json:"file_id"`
	Path    string     `json:"path"`
	Role    string     `json:"role"`
	Content ContentRef `json:"content_ref"`
}
type Selection struct {
	Purpose string   `json:"purpose"`
	FileIDs []string `json:"file_ids"`
}
type AssetRef struct {
	AssetID   string `json:"asset_id"`
	VersionID string `json:"asset_version_id"`
	Purpose   string `json:"purpose"`
}
type RunRef struct {
	ServiceID string `json:"service_id"`
	RunID     string `json:"run_id"`
}
type DomainDocument struct {
	Type   string `json:"document_type"`
	FileID string `json:"file_id"`
}
type Manifest struct {
	SchemaVersion   int                        `json:"schema_version"`
	Kind            string                     `json:"kind"`
	ProjectID       string                     `json:"project_id"`
	Name            string                     `json:"name"`
	Category        string                     `json:"category"`
	Status          string                     `json:"status"`
	EntryDocumentID string                     `json:"entry_document_id"`
	Files           []File                     `json:"files"`
	Selections      []Selection                `json:"selections"`
	AssetRefs       []AssetRef                 `json:"asset_refs"`
	RunRefs         []RunRef                   `json:"run_refs"`
	DomainDocuments []DomainDocument           `json:"domain_documents"`
	Extensions      map[string]json.RawMessage `json:"extensions,omitempty"`
}

func ValidID(s string) bool { return idPattern.MatchString(s) }
func validText(s string) bool {
	return utf8.ValidString(s) && strings.TrimSpace(s) != "" && utf8.RuneCountInString(s) <= 256
}

func (r ContentRef) Valid() bool {
	return ValidID(r.StoreID) && ValidID(r.ArtifactID) && ValidID(r.VersionID)
}

// PortablePath rejects names that cannot safely be materialized on supported clients.
// Actual filesystem materialization must additionally stay inside an os.Root and
// must not overwrite existing user files without an explicit conflict decision.
func PortablePath(s string) error {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > 1024 || !norm.NFC.IsNormalString(s) {
		return errors.New("path must be NFC UTF-8 within 1024 characters")
	}
	for _, c := range s {
		if unicode.IsControl(c) || strings.ContainsRune(`\<>:"|?*`, c) {
			return errors.New("path contains a forbidden character")
		}
	}
	for i, segment := range strings.Split(s, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.HasSuffix(segment, ".") || strings.HasSuffix(segment, " ") || devicePattern.MatchString(segment) {
			return errors.New("path contains an invalid segment")
		}
		if i == 0 && strings.EqualFold(segment, ".vf") {
			return errors.New("path uses the reserved management directory")
		}
	}
	return nil
}

// PathKey folds Unicode simple-case equivalence, including non-ASCII names.
func PathKey(s string) string {
	return strings.Map(func(r rune) rune {
		min := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < min {
				min = next
			}
		}
		return min
	}, s)
}

func (m Manifest) Validate() error {
	return m.validate(nil)
}

// pending is private preparation state, never accepted by public manifest decoding.
func (m Manifest) validate(pending map[string]bool) error {
	if m.SchemaVersion != 1 || m.Kind != "project" || !ValidID(m.ProjectID) || !validText(m.Name) || !tokenPattern.MatchString(m.Category) {
		return errors.New("invalid project identity or format")
	}
	switch m.Status {
	case "draft", "active", "paused", "acceptance", "completed", "archived":
	default:
		return errors.New("invalid project status")
	}
	if len(m.Files) < 1 || len(m.Files) > MaxFiles || m.Selections == nil || m.AssetRefs == nil || m.RunRefs == nil || m.DomainDocuments == nil {
		return errors.New("manifest requires bounded files and all inventory arrays")
	}
	files := make(map[string]File, len(m.Files))
	paths := make(map[string]bool, len(m.Files))
	for _, f := range m.Files {
		if !ValidID(f.ID) || !tokenPattern.MatchString(f.Role) || (!f.Content.Valid() && !pending[f.ID]) {
			return errors.New("invalid file identity, role or content reference")
		}
		if err := PortablePath(f.Path); err != nil {
			return err
		}
		key := PathKey(f.Path)
		if _, ok := files[f.ID]; ok || paths[key] {
			return errors.New("duplicate file identity or portable path")
		}
		files[f.ID], paths[key] = f, true
	}
	entry, ok := files[m.EntryDocumentID]
	if !ok || !(strings.HasSuffix(strings.ToLower(entry.Path), ".md") || strings.HasSuffix(strings.ToLower(entry.Path), ".txt")) {
		return errors.New("entry document must reference an inventoried MD or TXT file")
	}
	purposes := map[string]bool{}
	for _, s := range m.Selections {
		if !validText(s.Purpose) || purposes[s.Purpose] || len(s.FileIDs) == 0 {
			return errors.New("invalid or duplicate selection purpose")
		}
		purposes[s.Purpose] = true
		seen := map[string]bool{}
		for _, id := range s.FileIDs {
			if _, ok := files[id]; !ok || seen[id] {
				return errors.New("selection references a missing or duplicate file")
			}
			seen[id] = true
		}
	}
	documents := map[string]bool{}
	for _, d := range m.DomainDocuments {
		if _, ok := files[d.FileID]; !ok || !tokenPattern.MatchString(d.Type) || documents[d.Type] {
			return errors.New("invalid or duplicate domain document")
		}
		documents[d.Type] = true
	}
	assets := map[AssetRef]bool{}
	for _, a := range m.AssetRefs {
		if !ValidID(a.AssetID) || !ValidID(a.VersionID) || !validText(a.Purpose) || assets[a] {
			return errors.New("invalid or duplicate asset reference")
		}
		assets[a] = true
	}
	runs := map[RunRef]bool{}
	for _, r := range m.RunRefs {
		if !tokenPattern.MatchString(r.ServiceID) || !validText(r.RunID) || runs[r] {
			return errors.New("invalid or duplicate native run reference")
		}
		runs[r] = true
	}
	for key := range m.Extensions {
		if !extensionPattern.MatchString(key) {
			return errors.New("extension keys require a namespace")
		}
	}
	return nil
}

// Decode enforces strict JSON, including duplicate-key rejection before decoding
// into Go structs (encoding/json alone accepts duplicates and case variants).
func Decode(data []byte) (Manifest, error) {
	var m Manifest
	if len(data) > MaxManifestBytes || !utf8.Valid(data) {
		return m, errors.New("invalid manifest encoding or size")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := uniqueJSON(dec, 0); err != nil {
		return m, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return m, errors.New("trailing JSON data")
	}
	dec = json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, errors.New("manifest shape does not match contract")
	}
	// Exact property casing is checked separately: encoding/json accepts File_ID.
	if err := exactKeys(data); err != nil {
		return Manifest{}, err
	}
	return m, m.Validate()
}

func uniqueJSON(dec *json.Decoder, depth int) error {
	if depth > 64 {
		return errors.New("JSON nesting exceeds limit")
	}
	t, err := dec.Token()
	if err != nil {
		return errors.New("invalid JSON")
	}
	d, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch d {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return errors.New("invalid JSON key")
			}
			s, ok := key.(string)
			if !ok || seen[s] {
				return errors.New("duplicate JSON key")
			}
			seen[s] = true
			if err := uniqueJSON(dec, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for dec.More() {
			if err := uniqueJSON(dec, depth+1); err != nil {
				return err
			}
		}
	default:
		return errors.New("invalid JSON delimiter")
	}
	_, err = dec.Token()
	return err
}

func exactKeys(data []byte) error {
	var in map[string]json.RawMessage
	if err := json.Unmarshal(data, &in); err != nil {
		return errors.New("manifest must be an object")
	}
	allowed := map[string]bool{}
	for _, k := range strings.Fields("schema_version kind project_id name category status entry_document_id files selections asset_refs run_refs domain_documents extensions") {
		allowed[k] = true
	}
	for k := range in {
		if !allowed[k] {
			return errors.New("unknown manifest property")
		}
	}
	if raw, ok := in["extensions"]; ok && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("extensions must be an object")
	}
	groups := map[string]string{"files": "file_id path role content_ref", "selections": "purpose file_ids", "asset_refs": "asset_id asset_version_id purpose", "run_refs": "service_id run_id", "domain_documents": "document_type file_id"}
	for group, keys := range groups {
		var items []map[string]json.RawMessage
		if err := json.Unmarshal(in[group], &items); err != nil {
			return errors.New("invalid inventory array")
		}
		for _, item := range items {
			for k := range item {
				if !strings.Contains(" "+keys+" ", " "+k+" ") {
					return fmt.Errorf("unknown %s property", group)
				}
			}
			if group == "files" {
				var content map[string]json.RawMessage
				if err := json.Unmarshal(item["content_ref"], &content); err != nil {
					return errors.New("invalid content reference")
				}
				for k := range content {
					if k != "store_id" && k != "artifact_id" && k != "version_id" {
						return errors.New("unknown content reference property")
					}
				}
			}
		}
	}
	return nil
}

// ReconcileSelections prevents an old approval from silently attaching to changed
// bytes. Explicitly reselected purposes are checked against the new inventory.
func ReconcileSelections(previous, proposed Manifest, reselected []string) (Manifest, []string, error) {
	return reconcileSelections(previous, proposed, reselected, nil)
}
func reconcileSelections(previous, proposed Manifest, reselected []string, pending map[string]bool) (Manifest, []string, error) {
	if previous.ProjectID != proposed.ProjectID {
		return Manifest{}, nil, errors.New("project identity cannot change")
	}
	oldFiles, newFiles := map[string]ContentRef{}, map[string]ContentRef{}
	for _, f := range previous.Files {
		oldFiles[f.ID] = f.Content
	}
	for _, f := range proposed.Files {
		newFiles[f.ID] = f.Content
	}
	explicit := map[string]bool{}
	for _, p := range reselected {
		if !validText(p) || explicit[p] {
			return Manifest{}, nil, errors.New("invalid reselection purpose")
		}
		explicit[p] = true
	}
	invalid := map[string]bool{}
	for _, s := range previous.Selections {
		for _, id := range s.FileIDs {
			if newFiles[id] != oldFiles[id] {
				invalid[s.Purpose] = true
			}
		}
	}
	proposed.Selections = append([]Selection{}, proposed.Selections...)
	kept := make([]Selection, 0, len(proposed.Selections))
	cleared := []string{}
	for _, s := range proposed.Selections {
		if invalid[s.Purpose] && !explicit[s.Purpose] {
			cleared = append(cleared, s.Purpose)
			continue
		}
		kept = append(kept, s)
	}
	proposed.Selections = kept
	return proposed, cleared, proposed.validate(pending)
}
