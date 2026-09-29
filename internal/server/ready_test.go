package server_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/server"
)

func statusServer(t *testing.T, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
	t.Cleanup(srv.Close)
	return srv
}

func TestModelsURL(t *testing.T) {
	for in, want := range map[string]string{
		"http://127.0.0.1:8009":       "http://127.0.0.1:8009/v1/models",
		"http://127.0.0.1:8009/":      "http://127.0.0.1:8009/v1/models",
		"https://host.example/base/":  "https://host.example/base/v1/models",
		"http://localhost:8080?x=1#y": "http://localhost:8080/v1/models",
	} {
		got, err := server.ModelsURL(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	for _, bad := range []string{"", "127.0.0.1:8009", "ftp://h", "http://"} {
		_, err := server.ModelsURL(bad)
		assert.Error(t, err, bad)
	}
}

func TestReadyAcceptsAnyStatusBelowFiveHundred(t *testing.T) {
	for status, want := range map[int]bool{200: true, 204: true, 301: true, 401: true, 404: true, 499: true, 500: false, 503: false} {
		srv := statusServer(t, status)
		assert.Equal(t, want, server.Ready(t.Context(), srv.URL), status)
	}
}

func TestProbeReturnsTheStatusAndDoesNotFollowRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	defer srv.Close()
	status, err := server.Probe(t.Context(), srv.URL)
	require.NoError(t, err)
	assert.Equal(t, http.StatusFound, status)
}

func TestReadyIsFalseWhenNothingListens(t *testing.T) {
	assert.False(t, server.Ready(t.Context(), "http://127.0.0.1:"+strconv.Itoa(freePort(t))+"/v1/models"))
}

func TestProbeStopsWithinTwoSecondsOfASilentServer(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-block }))
	defer srv.Close()
	defer close(block)

	started := time.Now()
	_, err := server.Probe(t.Context(), srv.URL)
	require.Error(t, err)
	assert.Less(t, time.Since(started), 4*time.Second)
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())
	return port
}

func TestLogTail(t *testing.T) {
	path := writeLog(t, "one\ntwo\nthree\nfour\n")
	tail, err := server.LogTail(path, 2)
	require.NoError(t, err)
	assert.Equal(t, "three\nfour", tail)

	tail, err = server.LogTail(path, 10)
	require.NoError(t, err)
	assert.Equal(t, "one\ntwo\nthree\nfour", tail)

	tail, err = server.LogTail(path, 0)
	require.NoError(t, err)
	assert.Empty(t, tail)

	_, err = server.LogTail(path+".missing", 5)
	assert.Error(t, err)
}

func TestLogTailOfALargeFileReadsOnlyTheEnd(t *testing.T) {
	var content []byte
	for i := range 20000 {
		content = append(content, []byte("line "+strconv.Itoa(i)+"\n")...)
	}
	path := writeLog(t, string(content))
	tail, err := server.LogTail(path, 3)
	require.NoError(t, err)
	assert.Equal(t, "line 19997\nline 19998\nline 19999", tail)
}

func TestLogTailHandlesAnEmptyFile(t *testing.T) {
	tail, err := server.LogTail(writeLog(t, ""), 5)
	require.NoError(t, err)
	assert.Empty(t, tail)
}

func TestParsePS(t *testing.T) {
	const chrome = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	cases := map[string]struct {
		line   string
		pgid   int
		zombie bool
		text   string
		ok     bool
	}{
		"single spaces":           {"Tue Sep 30 10:00:00 2026 Ss 1234 " + chrome + " --x", 1234, false, chrome + " --x", true},
		"macos right aligned":     {"Tue Sep 30 10:00:00 2026 Ss      1234 " + chrome + " --x", 1234, false, chrome + " --x", true},
		"padded day and leading":  {" Tue Sep  3 10:00:00 2026 S+     77 /bin/sleep 60", 77, false, "/bin/sleep 60", true},
		"many spaces in command":  {"Tue Sep 30 10:00:00 2026 R   5 prog   a    b   c", 5, false, "prog   a    b   c", true},
		"tabs between fields":     {"Tue Sep 30 10:00:00 2026	Ss	 9	prog arg", 9, false, "prog arg", true},
		"zombie":                  {"Tue Sep 30 10:00:00 2026 Z+ 99 (defunct)", 99, true, "(defunct)", true},
		"no command":              {"Tue Sep 30 10:00:00 2026 Ss 4", 4, false, "", true},
		"garbage":                 {"garbage", 0, false, "", false},
		"bad date":                {"Xxx Sep 30 10:00:00 2026 Ss 1234 prog", 0, false, "", false},
		"pgid is not a number":    {"Tue Sep 30 10:00:00 2026 Ss abc prog", 0, false, "", false},
		"state only":              {"Tue Sep 30 10:00:00 2026 Ss", 0, false, "", false},
		"nothing after the start": {"Tue Sep 30 10:00:00 2026", 0, false, "", false},
	}
	for name, tc := range cases {
		ticks, pgid, zombie, text, ok := server.ParsePS(tc.line)
		require.Equal(t, tc.ok, ok, name)
		if !ok {
			continue
		}
		assert.NotZero(t, ticks, name)
		assert.Equal(t, tc.pgid, pgid, name)
		assert.Equal(t, tc.zombie, zombie, name)
		assert.Equal(t, tc.text, text, name)
	}
}

func TestParseStat(t *testing.T) {
	state, group, start, ok := server.ParseStat("4242 (my (odd) name) S 1 4242 4242 0 -1 4194560 100 0 0 0 1 2 0 0 20 0 1 0 987654 1000 100 18446744073709551615")
	require.True(t, ok)
	assert.Equal(t, "S", state)
	assert.Equal(t, 4242, group)
	assert.Equal(t, uint64(987654), start)

	_, _, _, ok = server.ParseStat("4242 (short) S 1")
	assert.False(t, ok)
}

func writeLog(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "x.log")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}
