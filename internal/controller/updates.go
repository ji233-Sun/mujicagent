package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/netproxy"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

const releaseEndpoint = "https://api.github.com/repos/ji233-Sun/mujicagent/releases?per_page=20"

type AppUpdateState struct {
	Version                              string
	Enabled, Checking, Installing, Ready bool
	CheckedAt                            time.Time
	Downloaded, Total                    int64
	Releases                             []model.Release
	AvailableVersion, Error              string
	update                               *mygo.Update
}

// Start once after the window exists; all state changes go through the UI queue.
func (c *Controller) StartAppUpdates() {
	c.AppUpdates.Version = mygo.App.Version()
	c.AppUpdates.Enabled = mygo.Updater.Enabled()
	go func() {
		timer := time.NewTimer(10 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-c.stop:
				return
			case <-timer.C:
				c.post(c.CheckAppUpdates)
				timer.Reset(24 * time.Hour)
			}
		}
	}()
}

func (c *Controller) CheckAppUpdates() {
	u := &c.AppUpdates
	if u.Checking || u.Installing || u.Ready {
		return
	}
	u.Checking, u.Error = true, ""
	u.update, u.AvailableVersion = nil, ""
	enabled := u.Enabled
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		client := &http.Client{Transport: netproxy.Transport()}
		releases, err := fetchReleases(ctx, client, releaseEndpoint)
		var update *mygo.Update
		// An empty repository has no update manifest yet.
		if err == nil && enabled && len(releases) > 0 {
			// Resolve the manifest from the same release as the displayed notes.
			// A separately cached /latest redirect may still point to an older release.
			mygo.Updater.SetSource(releaseUpdateSource{tag: releases[0].Tag, client: client})
			update, err = mygo.Updater.Check(ctx)
			mygo.Updater.SetSource(nil)
		}
		c.post(func() {
			u.Checking = false
			if releases != nil {
				u.Releases = releases
			}
			if err != nil {
				u.Error = err.Error()
				return
			}
			u.Releases, u.update, u.CheckedAt = releases, update, time.Now()
			u.AvailableVersion = ""
			if update != nil {
				u.AvailableVersion = update.Version
			}
		})
	}()
}

// MyGo retains this source on the returned Update and still verifies the archive signature.
type releaseUpdateSource struct {
	tag    string
	client *http.Client
}

func (s releaseUpdateSource) Manifest(ctx context.Context, _ bool) (io.ReadCloser, error) {
	endpoint := "https://github.com/ji233-Sun/mujicagent/releases/download/" + url.PathEscape(s.tag) + "/update-" + runtime.GOOS + "-" + runtime.GOARCH + ".json"
	body, err := s.Open(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	const maxManifestSize = 1 << 20
	data, err := io.ReadAll(io.LimitReader(body, maxManifestSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxManifestSize {
		return nil, fmt.Errorf("update manifest exceeds 1 MiB")
	}
	var manifest struct{ Version string }
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	if strings.TrimPrefix(manifest.Version, "v") != strings.TrimPrefix(s.tag, "v") {
		return nil, fmt.Errorf("release %s has update manifest version %q", s.tag, manifest.Version)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s releaseUpdateSource) Open(ctx context.Context, endpoint string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if req.URL.Scheme != "https" {
		return nil, fmt.Errorf("update URL must use HTTPS")
	}
	req.Header.Set("User-Agent", "MujicAgent")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GitHub update: HTTP %d", resp.StatusCode)
	}
	return resp.Body, nil
}

func (c *Controller) InstallAppUpdate() {
	u := &c.AppUpdates
	if !u.Enabled || u.update == nil || u.Installing || u.Checking || u.Ready || c.AnyRunning() {
		return
	}
	u.Installing, u.Error, u.Downloaded, u.Total = true, "", 0, 0
	update := u.update
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		last := time.Time{}
		err := update.Install(ctx, func(done, total int64) {
			if done != total && time.Since(last) < 100*time.Millisecond {
				return
			}
			last = time.Now()
			c.post(func() { u.Downloaded, u.Total = done, total })
		})
		c.post(func() {
			u.Installing = false
			if err != nil {
				u.Error = err.Error()
				return
			}
			u.Ready = true
		})
	}()
}

func (c *Controller) RestartForUpdate() {
	if !c.AppUpdates.Ready || c.AnyRunning() {
		return
	}
	if err := c.saveState(); err != nil {
		c.AppUpdates.Error = err.Error()
		return
	}
	mygo.App.Relaunch()
}

func fetchReleases(ctx context.Context, client *http.Client, endpoint string) ([]model.Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "MujicAgent")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub Releases: HTTP %d", resp.StatusCode)
	}
	var all []model.Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&all); err != nil {
		return nil, err
	}
	releases := make([]model.Release, 0, len(all))
	for _, release := range all {
		if !release.Draft && !release.Prerelease && release.Tag != "" {
			releases = append(releases, release)
		}
	}
	return releases, nil
}
