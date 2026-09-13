// Package output renders normalized results for humans and machines.
//
// JSON mode is a public contract: the top-level envelope is
//
//	{"ok": true, "data": ..., "meta": ..., "warnings": []}
//
// on success and
//
//	{"ok": false, "error": {"code": ..., "message": ..., "action": ...}}
//
// on failure.
package output

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/morgancrozier/searchprobe/internal/gscerr"
)

// Warning is a structured, non-fatal notice attached to a result.
type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Envelope is the stable top-level JSON structure.
type Envelope struct {
	OK       bool
	Data     any
	Meta     any
	Warnings []Warning
	Error    *gscerr.Error
}

type successEnvelope struct {
	OK       bool      `json:"ok"`
	Data     any       `json:"data"`
	Meta     any       `json:"meta,omitempty"`
	Warnings []Warning `json:"warnings"`
}

type failureEnvelope struct {
	OK    bool          `json:"ok"`
	Error *gscerr.Error `json:"error"`
}

// MarshalJSON emits the success or failure shape documented in
// docs/ARCHITECTURE.md. Success always carries a warnings array; failure
// carries only ok and error.
func (e Envelope) MarshalJSON() ([]byte, error) {
	var v any
	if e.OK {
		w := e.Warnings
		if w == nil {
			w = []Warning{}
		}
		v = successEnvelope{OK: true, Data: e.Data, Meta: e.Meta, Warnings: w}
	} else {
		err := e.Error
		if err == nil {
			err = gscerr.New(gscerr.CodeInternal, "Unknown error.", "")
		}
		v = failureEnvelope{OK: false, Error: err}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// Success builds a success envelope. Warnings is always a non-nil array.
func Success(data, meta any, warnings []Warning) Envelope {
	if warnings == nil {
		warnings = []Warning{}
	}
	return Envelope{OK: true, Data: data, Meta: meta, Warnings: warnings}
}

// Failure builds an error envelope from any error.
func Failure(err error) Envelope {
	return Envelope{OK: false, Error: gscerr.From(err)}
}

// WriteJSON writes an envelope as indented JSON followed by a newline.
func WriteJSON(w io.Writer, env Envelope) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(env)
}
