package world

import (
	"github.com/verdantflarehub/verdantflare-app-world/internal/strictjson"
	"strings"
	"testing"
)

func TestOrganizationGrantValidation(t *testing.T) {
	p := Principal{OrganizationID: testID(30), SubjectID: testID(31), RequestID: testID(32)}
	for _, c := range []struct {
		scope, subject, role string
		valid                bool
	}{
		{"", testID(33), "reader", true}, {"subject", testID(33), "editor", true}, {"organization", "", "reader", true}, {"organization", "", "none", true},
		{"organization", "", "editor", false}, {"organization", testID(33), "reader", false}, {"subject", p.OrganizationID, "editor", false}, {"", p.OrganizationID, "reader", false}, {"other", "", "reader", false}, {"subject", "", "reader", false},
	} {
		_, e := worldGrantSubject(p, c.scope, c.subject, c.role)
		if (e == nil) != c.valid {
			t.Errorf("%+v: %v", c, e)
		}
	}
	for _, body := range []string{
		`{"asset_id":"` + testID(34) + `","scope":"organization","role":"reader","subject_id":""}`,
		`{"asset_id":"` + testID(34) + `","scope":null,"role":"reader"}`,
	} {
		var r WorldGrantRequest
		if strictjson.Decode(strings.NewReader(body), 4096, &r) == nil {
			t.Fatal("invalid shape accepted")
		}
	}
	var r WorldGrantRequest
	if e := strictjson.Decode(strings.NewReader(`{"asset_id":"`+testID(34)+`","scope":"organization","role":"reader"}`), 4096, &r); e != nil {
		t.Fatal(e)
	}
}
