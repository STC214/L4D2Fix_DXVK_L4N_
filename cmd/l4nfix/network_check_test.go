//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type networkProbeBody struct {
	reads  int
	closed bool
	empty  bool
}

func (b *networkProbeBody) Read(p []byte) (int, error) {
	b.reads += len(p)
	if b.empty {
		return 0, io.EOF
	}
	p[0] = 'P'
	return 1, nil
}
func (b *networkProbeBody) Close() error { b.closed = true; return nil }

func TestNetworkProbeBoundedAndClosed(t *testing.T) {
	for _, status := range []int{200, 206, 403, 429, 500} {
		for _, empty := range []bool{false, true} {
			t.Run(fmt.Sprint(status, empty), func(t *testing.T) {
				body := &networkProbeBody{empty: empty}
				c := &http.Client{Transport: workshopTransport(func(r *http.Request) (*http.Response, error) {
					if r.Method != "GET" || r.Header.Get("Range") != "bytes=0-0" {
						return nil, fmt.Errorf("not a bounded GET")
					}
					return &http.Response{StatusCode: status, Header: make(http.Header), Body: body}, nil
				})}
				err := probeDownload(context.Background(), c, "https://cdn.steamusercontent.com/test")
				wantPass := (status == 200 || status == 206) && !empty
				if (err == nil) != wantPass || !body.closed || body.reads > 1 {
					t.Fatalf("err=%v body=%+v", err, body)
				}
			})
		}
	}
}

func TestNetworkCheckAllResourcesAndIndependentFailures(t *testing.T) {
	for _, failure := range []string{"", "steam", "github", "preset", "missing", "badhost", "empty"} {
		t.Run(failure, func(t *testing.T) {
			var mu sync.Mutex
			seen := map[string]int{}
			var details []workshopDetail
			platforms := append([]workshopPlatform(nil), workshopPlatforms...)
			platforms = append(platforms, workshopPlatform{"preset", "素材", skeetoModIDs[0]})
			for i, p := range platforms {
				d := workshopDetail{ID: p.ID, Result: 1, AppID: 550, Content: fmt.Sprint(i + 100), Updated: 1, Size: 123,
					URL: "https://cdn.steamusercontent.com/" + p.Kind}
				if failure == "missing" && p.Kind == "evolved" {
					continue
				}
				if failure == "badhost" && p.Kind == "l4n" {
					d.URL = "https://untrusted.invalid/file"
				}
				details = append(details, d)
			}
			c := &http.Client{Transport: workshopTransport(func(r *http.Request) (*http.Response, error) {
				if err := r.Context().Err(); err != nil {
					return nil, err
				}
				mu.Lock()
				seen[r.URL.Host+r.URL.Path]++
				mu.Unlock()
				status, payload := 200, "P"
				switch r.URL.Host {
				case "api.steampowered.com":
					if failure == "steam" {
						return nil, fmt.Errorf("TLS handshake timeout")
					}
					if err := r.ParseForm(); err != nil || r.Form.Get("itemcount") != "4" {
						return nil, fmt.Errorf("missing four Steam IDs")
					}
					b, _ := json.Marshal(map[string]any{"response": map[string]any{"publishedfiledetails": details}})
					payload = string(b)
				case "api.github.com":
					if failure == "github" {
						status = 429
					} else {
						b, _ := json.Marshal(dxvkUpdateRelease([]byte("archive")))
						payload = string(b)
					}
				default:
					if r.Header.Get("Range") != "bytes=0-0" {
						return nil, fmt.Errorf("full download requested")
					}
					if failure == "preset" && r.URL.Path == "/preset" {
						status = 403
					}
					if failure == "empty" {
						payload = ""
					}
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(payload))}, nil
			})}
			reports := 0
			err := checkResourceNetwork(context.Background(), c, c, workshopAPI, dxvkReleaseAPI, func(_ string, _ error, _ time.Duration) { reports++ })
			if (err == nil) != (failure == "") {
				t.Fatalf("failure=%s err=%v", failure, err)
			}
			if failure == "" && reports != 7 {
				t.Fatalf("missing reports: %d", reports)
			}
			if seen["api.github.com/repos/doitsujin/dxvk/releases/latest"] == 0 || seen["api.steampowered.com/ISteamRemoteStorage/GetPublishedFileDetails/v1/"] == 0 {
				t.Fatal("one source skipped after the other failed")
			}
			if failure == "badhost" && seen["untrusted.invalid/file"] != 0 {
				t.Fatal("untrusted download host accessed")
			}
		})
	}
}

func TestNetworkCheckCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := &http.Client{Transport: workshopTransport(func(r *http.Request) (*http.Response, error) { return nil, r.Context().Err() })}
	if err := checkResourceNetwork(ctx, c, c, workshopAPI, dxvkReleaseAPI, nil); err == nil {
		t.Fatal("cancellation reported success")
	}
}

func TestNetworkProbeRejectsErrorPagesAndClosesBody(t *testing.T) {
	for _, contentType := range []string{"text/html; charset=UTF-8", "application/xhtml+xml", "application/json"} {
		body := &networkProbeBody{}
		c := &http.Client{Transport: workshopTransport(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{contentType}}, Body: body}, nil
		})}
		if err := probeDownload(context.Background(), c, "https://cdn.steamusercontent.com/test"); err == nil || !body.closed || body.reads != 0 {
			t.Fatalf("error page accepted/body leaked: %s %+v", contentType, body)
		}
	}
}

func TestWorkflowPrerequisites(t *testing.T) {
	for _, tc := range []struct {
		s     workflowState
		ready bool
	}{
		{workflowState{}, false},
		{workflowState{Network: checkPassed}, false},
		{workflowState{Updates: checkPassed, L4NReady: true}, false},
		{workflowState{Updates: checkRunning, L4NReady: true, DXVKReady: true}, false},
		{workflowState{Updates: checkPassed, L4NReady: true, DXVKReady: true}, true},
		// A platform failure must not discard verified base resources.
		{workflowState{Updates: checkFailed, L4NReady: true, DXVKReady: true}, true},
	} {
		if tc.s.canDeployBase() != tc.ready {
			t.Fatalf("wrong gate: %+v", tc)
		}
	}
}
