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

func TestPortInUse(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	assert.True(t, server.PortInUse(t.Context(), port))
	require.NoError(t, listener.Close())
	assert.False(t, server.PortInUse(t.Context(), port))
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

func TestParsePSAndParseStat(t *testing.T) {
	ticks, pgid, zombie, text, ok := server.ParsePS("Tue Sep 30 10:00:00 2026 Ss 1234 /Applications/Google Chrome.app/Contents/MacOS/Google Chrome --x")
	require.True(t, ok)
	assert.NotZero(t, ticks)
	assert.Equal(t, 1234, pgid)
	assert.False(t, zombie)
	assert.Equal(t, "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome --x", text)

	_, _, zombie, _, ok = server.ParsePS(" Tue Sep  3 10:00:00 2026 Z+ 99 (defunct)")
	require.True(t, ok)
	assert.True(t, zombie)

	_, _, _, _, ok = server.ParsePS("garbage")
	assert.False(t, ok)

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
