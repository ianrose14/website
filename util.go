package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
)

const (
	// By default, how much of the body of the response to include when stringifying an HTTP error.
	DefaultMaxErrorBody = 2048 // this is not a magic value, just feels like a good one

	// ditto, if the body is html.
	DefaultMaxHtmlErrorBody = 128 // this is not a magic value, just feels like a good one
)

// copied (in part) from googleapi.go
type Error struct {
	Code   int
	Body   string
	Header http.Header
}

func (e *Error) IsNotFound() bool {
	return e.Code == http.StatusNotFound
}

func (e *Error) Message() string {
	return e.Body
}

func (e *Error) StatusCode() int {
	return e.Code
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}

	if e.Header.Get("Content-Type") == "text/html" {
		return e.ErrorN(DefaultMaxHtmlErrorBody)
	} else {
		return e.ErrorN(DefaultMaxErrorBody)
	}
}

// ErrorN returns a string description of this error, with a custom limit on how much of the response body to include.
// A limit of -1 means "everything".
func (e *Error) ErrorN(maxBodyLen int) string {
	if maxBodyLen == -1 || maxBodyLen > len(e.Body) {
		return formatError(e.Code, e.Body)
	} else {
		return formatError(e.Code, e.Body[:maxBodyLen])
	}
}

func NewError(code int, body string) *Error {
	e := &Error{
		Code:   code,
		Body:   body,
		Header: make(http.Header),
	}

	if body == "" {
		e.Body = http.StatusText(e.Code)
	}

	return e
}

var _ error = &Error{}

func formatError(status int, body string) string {
	return fmt.Sprintf("HTTP error %d: %s", status, body)
}

// adapted from googleapi.go
func CheckResponse(rsp *http.Response) error {
	if rsp.StatusCode >= 200 && rsp.StatusCode <= 299 {
		return nil
	}
	body, err := ioutil.ReadAll(rsp.Body)
	if err != nil {
		body = []byte("[failed to read body]")
	}
	_ = rsp.Body.Close()

	return &Error{
		Code:   rsp.StatusCode,
		Body:   strings.TrimSpace(string(body)),
		Header: rsp.Header,
	}
}

func HttpError(w http.ResponseWriter, code int, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	if code < 500 {
		log.Printf("warning: %s", msg)
	} else {
		log.Printf("error: %s", msg)
	}

	http.Error(w, msg, code)
}

// DrainAndClose discards any remaining bytes in r, then closes r.
// You have to read responses fully to properly free up connections.
// See https://groups.google.com/forum/#!topic/golang-nuts/pP3zyUlbT00
func DrainAndClose(r io.ReadCloser) {
	_, _ = io.Copy(ioutil.Discard, r)
	_ = r.Close()
}

func FileExists(p string) bool {
	if _, err := os.Stat(p); err == nil {
		return true
	} else {
		return false
	}
}
