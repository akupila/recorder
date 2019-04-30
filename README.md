# recorder

[![GoDoc](https://img.shields.io/badge/godoc-reference-5272B4.svg?style=flat-square)](https://godoc.org/github.com/akupila/recorder)
[![CircleCI](https://circleci.com/gh/akupila/recorder.svg?style=svg)](https://circleci.com/gh/akupila/recorder)
[![goreportcard](https://goreportcard.com/badge/github.com/akupila/recorder)](https://goreportcard.com/report/github.com/akupila/recorder)

<!-- TOC GFM -->

- [Overview](#overview)
  - [Example usage](#example-usage)
  - [Modes](#modes)
  - [Filters](#filters)
    - [Remove header from request](#remove-header-from-request)
    - [Remove header from response](#remove-header-from-response)
    - [Custom](#custom)
  - [Prior art](#prior-art)
  - [License](#license)

<!-- /TOC -->

# Overview

The recorder is a small helper package, primarily intended to help with unit
tests. It is capable of recording and replaying traffic, avoiding real network
requests.

In the default mode, responses are read from disk, allowing the network
roundtrip to be avoided. This can be useful for a couple reasons:

- Faster
- Works offline
- Reduces side-effects on called APIs (such as when testing cloud service
  provider endpoints)

In addition, with the `Passthrough` mode, requests can be recorded an asserted
in unit tests.

Unless the mode is set to `Passthrough`, the request-response is recorded in a
`yml` on disk, such as:

```yaml
# request 0
# timestamp 2019-04-30 10:02:04 +0000 UTC
# roundtrip 398ms
request:
  method: POST
  url: https://jsonplaceholder.typicode.com/posts
  headers:
    Content-Type: application/json
response:
  status_code: 201
  headers:
    Cache-Control: no-cache
    Content-Length: '69'
    Content-Type: application/json; charset=utf-8
    Date: Tue, 30 Apr 2019 10:02:04 GMT
    Location: http://jsonplaceholder.typicode.com/posts/101
    Pragma: no-cache
  body: |-
    {
      "title": "hello",
      "body": "world",
      "userId": 1,
      "id": 101
    }
```

## Example usage

```go
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
```

## Modes

Modes allow granular control of behavior.

| Mode          | Behavior                                                                 |
| ------------- | ------------------------------------------------------------------------ |
| `Auto`        | Perform network requests if no stored file exists                        |
| `ReplayOnly`  | Do not allow network traffic, only return stored files                   |
| `Record`      | Always perform request and overwrite existing files                      |
| `Passthrough` | No files are saved on disk but requests can be retrieved with `Lookup()` |

If no mode is set, `Auto` is used.

The `Passthrough` mode disabled loading and saving files but can be useful for
asserting if the expected requests were made in tests.

## Filters

Filters allow removing sensitive data from the saved files.

The filters are executed **after** the request but **before** saving files to disk.

### Remove header from request

This will remove the `Authorization` header from the request:

```go
rec := recorder.New("testdata/private-api", recorder.RemoveRequestHeader("Authorization"))

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
```

### Remove header from response

This will remove the `Set-Cookie` header from the response:

```go
rec := recorder.New("testdata/private-api", recorder.RemoveResponseHeader("Set-Cookie"))

cli := &http.Client{
    Transport: rec,
}

_, err := cli.Get("https://example.com")
if err != nil {
    log.Fatal(err)
}

// The saved file will not contain the Set-Cookie header that was set by the server.
```

### Custom

In addition to the built in filters, custom filters can be implemented by
passing functions with a signature `func (entry *recorder.Entry) {}`.

```go
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
```

## Prior art

This library is inspired by:

- https://github.com/dnaeon/go-vcr
- https://github.com/ad2games/vcr-go
- https://github.com/nock/nock#recording

The reason for writing this library is primarily flexiblity in setting the mode
and different API (no VCR references).

## License

MIT
