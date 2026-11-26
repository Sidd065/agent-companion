package server

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
)

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func urlQueryEscape(s string) string { return url.QueryEscape(s) }

// limiter throttles clients that keep failing the handshake.
type limiter struct {
	mu    sync.Mutex
	fails map[string][]time.Time
}

func newLimiter() *limiter { return &limiter{fails: map[string][]time.Time{}} }

const (
	failWindow = time.Minute
	maxFails   = 20
)

func (l *limiter) prune(ip string, now time.Time) []time.Time {
	ts := l.fails[ip]
	i := 0
	for i < len(ts) && now.Sub(ts[i]) > failWindow {
		i++
	}
	ts = ts[i:]
	if len(ts) == 0 {
		delete(l.fails, ip)
	} else {
		l.fails[ip] = ts
	}
	return ts
}

func (l *limiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.prune(ip, time.Now())) < maxFails
}

func (l *limiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.fails[ip] = append(l.prune(ip, now), now)
	if len(l.fails) > 10000 { // bound memory under a flood
		for k := range l.fails {
			delete(l.fails, k)
			if len(l.fails) < 5000 {
				break
			}
		}
	}
}

// uploads holds in-progress chunked uploads (fs.uploadStart/Chunk/Finish).
type uploads struct {
	mu sync.Mutex
	m  map[string]*upload
}

type upload struct {
	mu        sync.Mutex
	f         *os.File
	tmp, dest string
	overwrite bool
	written   int64
	last      time.Time
	closed    bool
}

func newUploads(stopCh <-chan struct{}) *uploads {
	u := &uploads{m: map[string]*upload{}}
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-stopCh:
				u.mu.Lock()
				items := make([]*upload, 0, len(u.m))
				for id, up := range u.m {
					items = append(items, up)
					delete(u.m, id)
				}
				u.mu.Unlock()
				for _, up := range items {
					up.mu.Lock()
					if !up.closed {
						up.closed = true
						_ = up.f.Close()
						_ = os.Remove(up.tmp)
					}
					up.mu.Unlock()
				}
				return
			case <-t.C:
				var expired []*upload
				u.mu.Lock()
				for id, up := range u.m {
					up.mu.Lock()
					stale := time.Since(up.last) > 15*time.Minute
					up.mu.Unlock()
					if stale {
						expired = append(expired, up)
						delete(u.m, id)
					}
				}
				u.mu.Unlock()
				for _, up := range expired {
					up.mu.Lock()
					if !up.closed {
						up.closed = true
						_ = up.f.Close()
						_ = os.Remove(up.tmp)
					}
					up.mu.Unlock()
				}
			}
		}
	}()
	return u
}

func (u *uploads) start(dest string, overwrite, mkdirs bool) (string, error) {
	if mkdirs {
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return "", err
		}
	}
	if st, err := os.Stat(dest); err == nil {
		if st.IsDir() {
			return "", errors.New("a directory with that name exists")
		}
		if !overwrite {
			return "", errors.New("file exists")
		}
	}
	f, err := os.CreateTemp(filepath.Dir(dest), "."+filepath.Base(dest)+".upload-*")
	if err != nil {
		return "", err
	}
	idb := make([]byte, 12)
	_, _ = rand.Read(idb)
	id := b64url(idb)
	u.mu.Lock()
	u.m[id] = &upload{f: f, tmp: f.Name(), dest: dest, overwrite: overwrite, last: time.Now()}
	u.mu.Unlock()
	return id, nil
}

func (u *uploads) get(id string) (*upload, error) {
	u.mu.Lock()
	up := u.m[id]
	u.mu.Unlock()
	if up == nil {
		return nil, errors.New("upload not found (it may have expired)")
	}
	up.mu.Lock()
	if up.closed {
		up.mu.Unlock()
		return nil, errors.New("upload not found (it may have expired)")
	}
	up.last = time.Now()
	up.mu.Unlock()
	return up, nil
}

func (u *uploads) chunk(id string, offset int64, data []byte) (int64, error) {
	up, err := u.get(id)
	if err != nil {
		return 0, err
	}
	up.mu.Lock()
	defer up.mu.Unlock()
	if up.closed {
		return up.written, errors.New("upload not found (it may have expired)")
	}
	if offset != up.written {
		return up.written, errors.New("unexpected offset")
	}
	if _, err := up.f.Write(data); err != nil {
		return up.written, err
	}
	up.written += int64(len(data))
	up.last = time.Now()
	return up.written, nil
}

func (u *uploads) finish(id string) (string, int64, error) {
	u.mu.Lock()
	up := u.m[id]
	delete(u.m, id)
	u.mu.Unlock()
	if up == nil {
		return "", 0, errors.New("upload not found (it may have expired)")
	}
	up.mu.Lock()
	defer up.mu.Unlock()
	if up.closed {
		return "", 0, errors.New("upload not found (it may have expired)")
	}
	up.closed = true
	if err := up.f.Close(); err != nil {
		_ = os.Remove(up.tmp)
		return "", 0, err
	}
	if !up.overwrite {
		if _, err := os.Stat(up.dest); err == nil {
			_ = os.Remove(up.tmp)
			return "", 0, errors.New("file exists")
		}
	}
	_ = os.Chmod(up.tmp, 0o644)
	if err := os.Rename(up.tmp, up.dest); err != nil {
		_ = os.Remove(up.tmp)
		return "", 0, err
	}
	return up.dest, up.written, nil
}

func (u *uploads) abort(id string) {
	u.mu.Lock()
	up := u.m[id]
	delete(u.m, id)
	u.mu.Unlock()
	if up != nil {
		up.mu.Lock()
		if !up.closed {
			up.closed = true
			_ = up.f.Close()
			_ = os.Remove(up.tmp)
		}
		up.mu.Unlock()
	}
}
