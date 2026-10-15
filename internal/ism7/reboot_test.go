package ism7

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReboot_Request(t *testing.T) {
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		got = r
	}))
	defer srv.Close()
	require.NoError(t, Reboot(context.Background(), srv.URL, "pw"))
	assert.Equal(t, "POST", got.Method)
	assert.Equal(t, "/protect/reboot.htm", got.URL.Path)
	u, p, _ := got.BasicAuth()
	assert.Equal(t, "admin", u)
	assert.Equal(t, "pw", p)
	assert.Equal(t, "reboKickoff", got.PostForm.Get("name"))
	assert.Equal(t, "init", got.PostForm.Get("rebo"))
}

func TestReboot_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer srv.Close()
	assert.Error(t, Reboot(context.Background(), srv.URL, "bad"))
}
