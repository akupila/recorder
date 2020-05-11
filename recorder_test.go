package recorder_test

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/akupila/recorder"
	"github.com/google/go-cmp/cmp"
)

func TestMain(m *testing.M) {
	code := m.Run()
	if err := os.RemoveAll("testdata"); err != nil {
		log.Fatalf("Clean up testdata: %v", err)
	}
	os.Exit(code)
}

func Example() {
	// Create a new recorder.
	// Data will be saved in testdata/example.yml
	rec := recorder.New("testdata/example")

	// Create HTTP client with recorder transport
	cli := &http.Client{
		Transport: rec,
	}

	// Perform a request
	resp, err := cli.Get("https://jsonplaceholder.typicode.com/posts/1")
	if err != nil {
		log.Fatal(err)
	}

	// Response is only done if required
	b, err := httputil.DumpResponse(resp, true)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(b))
}

func ExampleFilter_custom() {
	rec := recorder.New("testdata/request-header", func(e *recorder.Entry) {
		// Modify e.Request and e.Response
	})

	cli := &http.Client{
		Transport: rec,
	}

	_, err := cli.Get("https://example.com")
	if err != nil {
		log.Fatal(err)
	}
}

func ExampleRemoveRequestHeader() {
	rec := recorder.New("testdata/request-header", recorder.RemoveRequestHeader("Authorization"))

	cli := &http.Client{
		Transport: rec,
	}

	req, _ := http.NewRequest("https://example.com", "application/json", strings.NewReader("{}"))
	req.Header.Add("Authorization", "abcdef")

	_, err := cli.Do(req)
	if err != nil {
		log.Fatal(err)
	}

	// Authorization header is not saved to disk
}

func ExampleRemoveResponseHeader() {
	rec := recorder.New("testdata/secret", recorder.RemoveResponseHeader("Set-Cookie"))

	cli := &http.Client{
		Transport: rec,
	}

	_, err := cli.Get("https://example.com")
	if err != nil {
		log.Fatal(err)
	}

	// The saved file will not contain the Set-Cookie header that was set by the server.
}

func ExampleNoRequestError() {
	rec := recorder.New("notfound")

	// Disallow network traffic so this returns an error.
	rec.Mode = recorder.ReplayOnly

	cli := &http.Client{Transport: rec}
	if _, err := cli.Get("https://example.com"); err != nil {
		uerr, ok := err.(*url.Error)
		if !ok {
			log.Fatal("Error is not *url.Error")
		}
		_, ok = uerr.Err.(recorder.NoRequestError)
		if ok {
			// Recorded entry was not found.
		}
	}
}

func TestRoundTrip_Default_replay(t *testing.T) {
	requests := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(200)
	}))
	defer ts.Close()

	rec := recorder.New("testdata/roundtrip-auto")
	cli := &http.Client{Transport: rec}

	for i := 0; i < 5; i++ {
		_, err := cli.Get(ts.URL)
		if err != nil {
			log.Fatal(err)
		}
	}

	if requests != 1 {
		t.Errorf("Got %d outgoing requests, want %d", requests, 1)
	}
}

func TestRoundTrip_RequestBody(t *testing.T) {
	body := []byte(`{"hello": "world"}`)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("Got method %s, want %s", r.Method, http.MethodPost)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Got content-type %s, want %s", r.Header.Get("Content-Type"), "application/json")
		}

		gotBody, err := ioutil.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
			return
		}
		if !bytes.Equal(gotBody, body) {
			t.Errorf("Body does not match\nGot  %s\nWant %s", gotBody, body)
		}

		w.WriteHeader(200)
	}))
	defer ts.Close()

	rec := recorder.New("testdata/roundtrip-post")
	cli := &http.Client{Transport: rec}

	_, err := cli.Post(ts.URL, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Fatal(err)
	}

	got, ok := rec.Lookup(http.MethodPost, ts.URL)
	if !ok {
		t.Fatalf("Entry was not recorded")
	}

	if got.Request.Body != string(body) {
		t.Errorf("Request body does not match\nGot  %s\nWant %s", got.Request.Body, string(body))
	}
}

func TestRoundTrip_ResponseBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("Got method %s, want %s", r.Method, http.MethodGet)
		}
		w.Write([]byte("hello")) // nolint: errcheck
	}))
	defer ts.Close()

	rec := recorder.New("testdata/roundtrip-get")
	cli := &http.Client{Transport: rec}

	resp, err := cli.Get(ts.URL)
	if err != nil {
		log.Fatal(err)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Read body: %v", err)
	}

	wantBody := []byte("hello")
	if !bytes.Equal(body, wantBody) {
		t.Errorf("Returned body does not match\nGot  %s\nWant %s", body, wantBody)
	}
}

func TestRoundTrip_ReplayOnly(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("Request was sent to server")
	}))
	defer ts.Close()

	rec := recorder.New("testdata/roundtrip-replay-only")
	rec.Mode = recorder.ReplayOnly

	cli := &http.Client{Transport: rec}

	_, err := cli.Get(ts.URL)
	if err != nil {
		uerr, ok := err.(*url.Error)
		if !ok {
			t.Fatalf("Returned error is %T, not *url.Error", err)
		}
		_, ok = uerr.Err.(recorder.NoRequestError)
		if !ok {
			t.Errorf("Got error %T %v, want %T", err, err, recorder.NoRequestError{})
		}
	}
}

func TestRoundTrip_Record(t *testing.T) {
	requests := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(200)
	}))
	defer ts.Close()

	rec := recorder.New("testdata/roundtrip-record")
	rec.Mode = recorder.Record

	cli := &http.Client{Transport: rec}

	n := 3
	for i := 0; i < n; i++ {
		_, err := cli.Get(ts.URL)
		if err != nil {
			log.Fatal(err)
		}
	}

	if requests != n {
		t.Errorf("Got %d outgoing requests, want %d", requests, n)
	}
}

func TestRoundTrip_Passthrough(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello")) // nolint: errcheck
	}))
	defer ts.Close()

	rec := recorder.New("testdata/passthrough")
	rec.Mode = recorder.Passthrough

	cli := &http.Client{Transport: rec}

	_, err := cli.Get(ts.URL)
	if err != nil {
		log.Fatal(err)
	}

	_, ok := rec.Lookup(http.MethodGet, ts.URL)
	if !ok {
		t.Fatalf("Entry was not recorded")
	}

	// Nothing should be saved
	_, err = os.Open("testdata/passthrough")
	if !os.IsNotExist(err) {
		t.Errorf("Data was recorded to disk")
	}
}

func TestRoundTrip_Data(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "hello")
		w.Write([]byte("hello")) // nolint: errcheck
	}))
	defer ts.Close()

	rec := recorder.New("testdata/data")

	cli := &http.Client{Transport: rec}

	req, err := http.NewRequest(http.MethodPost, ts.URL, strings.NewReader(`{"hello": "world"}`))
	if err != nil {
		log.Fatal(err)
	}
	req.Header.Add("Authorization", "abc")
	resp, err := cli.Do(req)
	if err != nil {
		log.Fatal(err)
	}

	want := recorder.Entry{
		Request: &recorder.Request{
			Method: http.MethodPost,
			URL:    ts.URL,
			Headers: map[string]string{
				"Authorization": "abc",
			},
			Body: `{"hello": "world"}`,
		},
		Response: &recorder.Response{
			StatusCode: 200,
			Headers: map[string]string{
				"Content-Length": "5",
				"Set-Cookie":     "hello",
				"Content-Type":   "text/plain; charset=utf-8",     // Added by
				"Date":           "Tue, 30 Apr 2019 11:09:11 GMT", // go stdlib
			},
			Body: "hello",
		},
	}

	// Check response
	if resp.StatusCode != want.Response.StatusCode {
		t.Errorf("Response status = %d, want = %d", resp.StatusCode, want.Response.StatusCode)
	}

	gotContent := resp.Header.Get("Content-Type")
	wantContent := want.Response.Headers["Content-Type"]
	if gotContent != wantContent {
		t.Errorf("Response content-type header does not match\nGot  %q\nWant %q", gotContent, wantContent)
	}

	gotBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Read body: %v", err)
	}
	if !bytes.Equal(gotBody, []byte(want.Response.Body)) {
		t.Errorf("Response body does not match\nGot  %s\nWant %s", string(gotBody), want.Response.Body)
	}

	// Check recorded entry
	got, ok := rec.Lookup(http.MethodPost, ts.URL)
	if !ok {
		t.Fatalf("Entry was not recorded")
	}

	opts := []cmp.Option{
		cmp.FilterPath(func(p cmp.Path) bool {
			return p.String() == "Response.Headers"
		}, cmp.Comparer(func(a, b map[string]string) bool {
			return len(a) == len(b)
		})),
	}
	if diff := cmp.Diff(got, want, opts...); diff != "" {
		t.Errorf("Returned entry does not match (-got, +want)\n%s", diff)
	}

	// Nothing should be saved
	_, err = os.Open("testdata/passthrough")
	if !os.IsNotExist(err) {
		t.Errorf("Data was recorded to disk")
	}
}

func TestRemoveRequestHeader(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(202)
	}))
	defer ts.Close()

	rec := recorder.New("testdata/req-header.yml", recorder.RemoveRequestHeader("Authorization"))
	cli := &http.Client{Transport: rec}

	req, _ := http.NewRequest(http.MethodPost, ts.URL, strings.NewReader(`{"hello": "world"}`))
	req.Header.Add("Authorization", "abc")

	_, err := cli.Do(req)
	if err != nil {
		log.Fatal(err)
	}

	saved, err := ioutil.ReadFile("testdata/req-header.yml")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(saved, []byte("Authorization")) {
		t.Errorf("Saved file contains auth header\n\n%s", string(saved))
	}
}

func TestRemoveResponseHeader(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Set-Cookie", "hello")
	}))
	defer ts.Close()

	rec := recorder.New("testdata/req-header.yml", recorder.RemoveResponseHeader("Set-Cookie"))
	cli := &http.Client{Transport: rec}

	_, err := cli.Get(ts.URL)
	if err != nil {
		log.Fatal(err)
	}

	saved, err := ioutil.ReadFile("testdata/req-header.yml")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(saved, []byte("Set-Cookie")) {
		t.Errorf("Saved file contains cookie header\n\n%s", string(saved))
	}
}

func TestFilterResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("oh, hello there!")) // nolint: errcheck
	}))
	defer ts.Close()

	rec := recorder.New("testdata/req-response-filter", func(e *recorder.Entry) {
		e.Response.Body = strings.Replace(e.Response.Body, "hello", "hi", -1)
	})
	cli := &http.Client{Transport: rec}

	resp, err := cli.Get(ts.URL)
	if err != nil {
		log.Fatal(err)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	wantBody := "oh, hi there!"
	if !bytes.Equal(body, []byte(wantBody)) {
		t.Errorf("Returned body does not match\nGot  %q\nWant %q", string(body), wantBody)
	}
}

type SelectorFunc func(entries []recorder.Entry, req *http.Request) (recorder.Entry, bool)

func (f SelectorFunc) Select(entries []recorder.Entry, req *http.Request) (recorder.Entry, bool) {
	return f(entries, req)
}

func TestSelect(t *testing.T) {
	// This test verifies that the Select function is being used if provided. It
	// sets up a recorder and test server and records 5 different calls to the
	// server.
	//
	// It then switches to playback mode and configures a selector that will
	// always choose the first entry, even if it doesn't otherwise match the
	// incoming request. The test then issues the same 5 different calls to the
	// recorder and verifies that it gets back the initial call repeated over and
	// over.

	serverCalls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		serverCalls++
		fmt.Fprintf(w, "call %d", serverCalls)
	}))
	defer ts.Close()

	rec := recorder.New("testdata/select")
	rec.Mode = recorder.Record

	cli := &http.Client{Transport: rec}
	for i := 1; i <= 5; i++ {
		cli.Get(fmt.Sprintf("%s/load/%d", ts.URL, i))
	}

	if serverCalls != 5 {
		t.Fatalf("Expect to have made 5 calls to the server, but only made %d", serverCalls)
	}

	// Now switch to replay only, with our custom selector.
	rec.Mode = recorder.ReplayOnly
	selectCalls := 0
	rec.Selector = SelectorFunc(func(entries []recorder.Entry, req *http.Request) (recorder.Entry, bool) {
		selectCalls++
		expectedPath := fmt.Sprintf("/load/%d", selectCalls)
		if req.URL.Path != expectedPath {
			t.Errorf("Selected select call %d to be requesting path %q but got %q",
				selectCalls, expectedPath, req.URL.Path)
		}
		return entries[0], true
	})

	for i := 1; i <= 5; i++ {
		resp, err := cli.Get(fmt.Sprintf("%s/load/%d", ts.URL, i))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		payload, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		// Our select always chooses the first call, so even though we're loading
		// different URLs, we should always get back the recorded value for call 1.
		if string(payload) != "call 1" {
			t.Errorf("Expected replayed request to respond with `call 1` but got %#q", payload)
		}
	}
	if selectCalls != 5 {
		t.Errorf("Expected 5 select calls, but got %d", selectCalls)
	}
}

func TestOncePerCall(t *testing.T) {
	entries := []recorder.Entry{
		{
			Request:  &recorder.Request{Method: "GET", URL: "http://foo.com/bar"},
			Response: &recorder.Response{Body: "1"},
		},
		{
			Request:  &recorder.Request{Method: "GET", URL: "http://foo.com/bar"},
			Response: &recorder.Response{Body: "2"},
		},
		{
			Request:  &recorder.Request{Method: "GET", URL: "http://foo.com/bar"},
			Response: &recorder.Response{Body: "3"},
		},
		{
			Request:  &recorder.Request{Method: "GET", URL: "http://foo.com/baz"},
			Response: &recorder.Response{Body: "baz!"},
		},
		{
			Request:  &recorder.Request{Method: "PUT", URL: "http://foo.com/baz"},
			Response: &recorder.Response{Body: "putbaz!"},
		},
	}

	testcases := []struct {
		Method, URL, ExpectedBody string
	}{
		{"GET", "http://foo.com/bar", "1"},
		{"GET", "http://foo.com/bar", "2"},
		{"GET", "http://foo.com/bar", "3"},
		{"GET", "http://foo.com/bar", ""}, // no matching request
		{"GET", "http://foo.com/baz", "baz!"},
		{"GET", "http://foo.com/baz", ""}, // no matching request
		{"PUT", "http://foo.com/baz", "putbaz!"},
		{"PUT", "http://foo.com/baz", ""}, // no matching request
	}

	var sel recorder.OncePerCall

	for _, test := range testcases {
		e, ok := sel.Select(entries, httptest.NewRequest(test.Method, test.URL, nil))
		if test.ExpectedBody == "" {
			if ok {
				t.Errorf("Expected no matching entry, but got %v", e)
			}
		} else if !ok {
			t.Errorf("Expected a matching entry, but didn't get one")
		} else if e.Response.Body != test.ExpectedBody {
			t.Errorf("Entry mismatch. Expected body %q, but got %q",
				test.ExpectedBody, e.Response.Body)
		}
	}
}
