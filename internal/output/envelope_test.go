package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/morgancrozier/searchprobe/internal/gscerr"
)

func TestSuccessEnvelope(t *testing.T) {
	var buf bytes.Buffer
	env := Success(map[string]int{"n": 1}, map[string]string{"site": "sc-domain:example.com"}, nil)
	if err := WriteJSON(&buf, env); err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if string(got["ok"]) != "true" {
		t.Errorf("ok = %s", got["ok"])
	}
	if string(got["warnings"]) != "[]" {
		t.Errorf("warnings = %s, want []", got["warnings"])
	}
	if _, has := got["error"]; has {
		t.Errorf("success envelope must not include error")
	}
	if !strings.Contains(buf.String(), `"site": "sc-domain:example.com"`) {
		t.Errorf("meta missing: %s", buf.String())
	}
}

func TestFailureEnvelope(t *testing.T) {
	var buf bytes.Buffer
	err := gscerr.New(gscerr.CodeAuthRequired, "Google Search Console authentication is required.", "Run `gsc auth login --client-file <path>`.")
	if werr := WriteJSON(&buf, Failure(err)); werr != nil {
		t.Fatal(werr)
	}
	var got struct {
		OK    bool `json:"ok"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Action  string `json:"action"`
		} `json:"error"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.OK || got.Error.Code != "AUTH_REQUIRED" || got.Error.Action == "" {
		t.Errorf("unexpected: %s", buf.String())
	}
	if strings.Contains(buf.String(), `"cause"`) {
		t.Errorf("cause must never be serialized")
	}
	if strings.Contains(buf.String(), `"warnings"`) || strings.Contains(buf.String(), `"data"`) {
		t.Errorf("failure envelope carries only ok and error: %s", buf.String())
	}
	if strings.Contains(buf.String(), `\u003c`) || !strings.Contains(buf.String(), "<path>") {
		t.Errorf("HTML escaping must be disabled: %s", buf.String())
	}
}

func TestFailureEnvelopeUnknownError(t *testing.T) {
	env := Failure(errors.New("boom"))
	if env.Error.Code != gscerr.CodeInternal || env.Error.Message != "boom" {
		t.Errorf("unexpected: %+v", env.Error)
	}
}
