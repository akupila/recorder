package recorder

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v2"
)

// NoRequestError is returned when the recorder mode is ReplayOnly and a
// corresponding entry is not found for the current request.
//
// Because the error is returned from the transport, it may be wrapped.
type NoRequestError struct{ Request *http.Request }

// Error implements the error interface.
func (e NoRequestError) Error() string { return fmt.Sprintf("no recorded entry") }

// Mode controls the mode of the recorder.
type Mode int

// Possible values:
const (
	// Auto reads requests from disk if a recording exists. If one does not
	// exist, the request is performed and results saved to disk.
	Auto Mode = iota

	// ReplayOnly only allows replaying from disk without network traffic.
	// If a recorded session does not exist, NoRequestError is returned.
	ReplayOnly

	// Record records all traffic even if an existing entry exists.
	// The new requests & responses overwrite any existing ones.
	Record

	// Passthrough disables the recorder and passes through all traffic
	// directly to client. Responses are not recorded to disk but can be
	// retrieved from the with Lookup().
	Passthrough
)

// Selector chooses a recorded Entry to response to a given request.
type Selector interface {
	Select(entries []Entry, req *http.Request) (Entry, bool)
}

// New is a convenience function for creating a new recorder.
func New(filename string, filters ...Filter) *Recorder {
	return &Recorder{
		Filename:  filename,
		Mode:      Auto,
		Transport: http.DefaultTransport,
		Filters:   filters,
	}
}

// Recorder wraps a http.RoundTripper by recording requests that go through it.
//
// When recording, any observed requests are written to disk after response. In
// case previous entries were recorded for the same endpoint, the file is
// overwritten on first request.
type Recorder struct {
	// Filename to use for saved entries. A .yml extension is added if not set.
	// Any subdirectories are created if needed.
	//
	// Required if mode is not Passthrough.
	Filename string

	// Mode to use. Default mode is Auto.
	Mode Mode

	// Filters to apply before saving to disk.
	// Filters are executed in the order specified.
	Filters []Filter

	// Transport to use for real request.
	// If nil, http.DefaultTransport is used.
	Transport http.RoundTripper

	// An optional Select function may be specified to control which recorded
	// Entry is selected to respond to a given request. If nil, the default
	// selection is used that picks the first recorded response with a matching
	// method and url.
	Selector Selector

	once    sync.Once
	index   int
	entries []Entry
}

var _ http.RoundTripper = (*Recorder)(nil)

func (r *Recorder) loadFromDisk() {
	if r.Mode == Passthrough {
		return
	}
	if !strings.HasSuffix(r.Filename, ".yml") {
		r.Filename += ".yml"
	}
	existing, err := ioutil.ReadFile(r.Filename)
	if err == nil {
		values := bytes.Split(existing, []byte("\n---\n"))
		for i, val := range values {
			var e Entry
			if err := yaml.Unmarshal(val, &e); err != nil {
				panic(fmt.Sprintf("unmarshal session %d from %s: %v", i, r.Filename, err))
			}
			r.entries = append(r.entries, e)
		}
	}
}

// RoundTrip implements http.RoundTripper and does the actual request.
//
// The behavior depends on the mode set:
//
//     Auto:          If an existing entry exists, the response from the entry
//                    is returned.
//     ReplayOnly:    Returns a previously recorded response. Returns
//                    NoRequestError if an entry is found for the request.
//     Record:        Always send real request and record the response. If an
//                    existing entry is found, it is overwritten.
//     Passthrough:   The request is passed through to the underlying
//                    transport.
//
// Attempting to set another mode will cause a panic.
func (r *Recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	if r.Mode > Passthrough {
		panic("Unsupported mode")
	}

	r.once.Do(r.loadFromDisk)

	if r.Mode == Auto || r.Mode == ReplayOnly {
		var e Entry
		var ok bool
		if r.Selector != nil {
			e, ok = r.Selector.Select(r.entries, req)
		} else {
			e, ok = r.Lookup(req.Method, req.URL.String())
		}
		if ok {
			resp := e.Response
			return &http.Response{
				StatusCode:    resp.StatusCode,
				Header:        expandHeader(resp.Headers),
				Body:          ioutil.NopCloser(strings.NewReader(resp.Body)),
				ContentLength: int64(len(e.Response.Body)),
			}, nil
		}
		if r.Mode == ReplayOnly {
			return nil, NoRequestError{Request: req}
		}
	}

	if r.Transport == nil {
		r.Transport = http.DefaultTransport
	}

	// Construct request
	var bodyOut bytes.Buffer
	if req.Body != nil {
		if _, err := io.Copy(&bodyOut, req.Body); err != nil {
			return nil, err
		}
	}
	req.Body = ioutil.NopCloser(&bodyOut)
	out := &Request{
		Method:  req.Method,
		URL:     req.URL.String(),
		Headers: flattenHeader(req.Header),
		Body:    bodyOut.String(),
	}
	for k, vv := range req.Header {
		out.Headers[k] = vv[0]
	}

	// Send request
	start := time.Now()
	resp, err := r.Transport.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	dur := time.Since(start)

	// Construct response
	in := &Response{
		StatusCode: resp.StatusCode,
		Headers:    flattenHeader(resp.Header),
	}
	bodyIn, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if err := resp.Body.Close(); err != nil {
		return nil, err
	}
	in.Body = string(bodyIn)

	// Construct entry
	e := Entry{Request: out, Response: in}

	// Apply filters
	for _, apply := range r.Filters {
		apply(&e)
	}

	// Reconstruct response after filters have been processed
	resp = &http.Response{
		StatusCode:    in.StatusCode,
		Header:        expandHeader(in.Headers),
		Body:          ioutil.NopCloser(strings.NewReader(in.Body)),
		ContentLength: int64(len(in.Body)),
	}

	// Save entry
	r.entries = append(r.entries, e)

	if r.Mode == Auto || r.Mode == Record {
		// Save to disk
		if err := os.MkdirAll(path.Dir(r.Filename), 0750); err != nil {
			return nil, err
		}

		var filemode int
		if r.index == 0 {
			filemode = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
		} else {
			filemode = os.O_WRONLY | os.O_APPEND
		}
		f, err := os.OpenFile(r.Filename, filemode, 0644)
		if err != nil {
			return nil, err
		}

		if r.index > 0 {
			fmt.Fprintf(f, "\n---\n\n")
		}
		fmt.Fprintf(f, "# request %d\n", r.index)
		fmt.Fprintf(f, "# timestamp %s\n", start.UTC().Round(time.Second))
		fmt.Fprintf(f, "# roundtrip %s\n", dur.Round(time.Millisecond))
		r.index++

		b, err := yaml.Marshal(e)
		if err != nil {
			return nil, err
		}
		if _, err := f.Write(b); err != nil {
			return nil, err
		}
		if err := f.Close(); err != nil {
			return nil, err
		}
	}

	return resp, nil
}

// Lookup returns an existing entry matching the given method and url.
//
// The method and url are case-insensitive.
//
// Returns false if no such entry exists.
func (r *Recorder) Lookup(method, url string) (Entry, bool) {
	r.once.Do(r.loadFromDisk)
	for _, e := range r.entries {
		if strings.EqualFold(e.Request.Method, method) && strings.EqualFold(e.Request.URL, url) {
			return e, true
		}
	}
	return Entry{}, false
}

// A Filter modifies the entry before it is saved to disk.
//
// Filters are applied after the actual request, with the primary purpose
// being to remove sensitive data from the saved file.
type Filter func(entry *Entry)

// RemoveRequestHeader removes a header with the given name from the request.
// The name of the header is case-sensitive.
func RemoveRequestHeader(name string) Filter {
	return func(e *Entry) {
		delete(e.Request.Headers, name)
	}
}

// RemoveResponseHeader removes a header with the given name from the response.
// The name of the header is case-sensitive.
func RemoveResponseHeader(name string) Filter {
	return func(e *Entry) {
		delete(e.Response.Headers, name)
	}
}

// An Entry is a single recorded request-response entry.
type Entry struct {
	Request  *Request  `yaml:"request"`
	Response *Response `yaml:"response"`
}

// A Request is a recorded outgoing request.
//
// The headers are flattened to a simple key-value map. The underlying request
// may contain multiple value for each key but in practice this is not very
// common and working with a simple key-value map is much more convenient.
type Request struct {
	Method  string            `yaml:"method"`
	URL     string            `yaml:"url"`
	Headers map[string]string `yaml:"headers,omitempty"`
	Body    string            `yaml:"body,omitempty"`
}

// A Response is a recorded incoming response.
//
// The headers are flattened to a simple key-value map. The underlying request
// may contain multiple value for each key but in practice this is not very
// common and working with a simple key-value map is much more convenient.
type Response struct {
	StatusCode int               `yaml:"status_code"`
	Headers    map[string]string `yaml:"headers,omitempty"`
	Body       string            `yaml:"body,omitempty"`
}

func flattenHeader(in http.Header) map[string]string {
	out := make(map[string]string, len(in))
	for k, vv := range in {
		out[k] = vv[0]
	}
	return out
}

func expandHeader(in map[string]string) http.Header {
	out := make(http.Header, len(in))
	for k, v := range in {
		out.Set(k, v)
	}
	return out
}

// OncePerCall is a Selector that selects entries based on the method and URL,
// but it will only select any given entry at most once.
type OncePerCall struct {
	mu   sync.Mutex
	used map[int]bool
}

// Select implements Selector and chooses an entry.
func (s *OncePerCall) Select(entries []Entry, req *http.Request) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.used == nil {
		s.used = map[int]bool{}
	}
	for i, e := range entries {
		if !strings.EqualFold(e.Request.Method, req.Method) {
			continue
		} else if !strings.EqualFold(e.Request.URL, req.URL.String()) {
			continue
		}
		if !s.used[i] {
			s.used[i] = true
			return e, true
		}
	}
	return Entry{}, false
}
