package panel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const gatewayApp = "ownpanel"
const gatewayPermission = "ownpanel.admin"
const challengeCookie = "__Host-ownpanel-login"

var grantCode = regexp.MustCompile(`^[A-Za-z0-9_-]{32,128}$`)

type publicAccess struct {
	origin                        string
	proxies                       []netip.Prefix
	gateway, internal, credential string
	client                        *http.Client
}

func checkedOrigin(raw string, localHTTP bool) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Path != "" && u.Path != "/" || u.ForceQuery || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("invalid origin")
	}
	ip, _ := netip.ParseAddr(u.Hostname())
	if u.Scheme != "https" && !(localHTTP && u.Scheme == "http" && ip.IsLoopback()) {
		return "", fmt.Errorf("HTTPS origin required")
	}
	return u.Scheme + "://" + u.Host, nil
}

func loadPublicAccess() (publicAccess, error) {
	var a publicAccess
	var err error
	raw := os.Getenv("OWNPANEL_PUBLIC_ORIGIN")
	proxies := os.Getenv("OWNPANEL_TRUSTED_PROXIES")
	gateway := os.Getenv("OWNPANEL_GATEWAY_URL")
	internal := os.Getenv("OWNPANEL_GATEWAY_INTERNAL_URL")
	file := os.Getenv("OWNPANEL_GATEWAY_TOKEN_FILE")
	if raw == "" && proxies == "" && gateway == "" && internal == "" && file == "" {
		return a, nil
	}
	if a.origin, err = checkedOrigin(raw, false); err != nil {
		return a, fmt.Errorf("OWNPANEL_PUBLIC_ORIGIN: %w", err)
	}
	for _, item := range strings.Split(proxies, ",") {
		item = strings.TrimSpace(item)
		p, e := netip.ParsePrefix(item)
		if e != nil {
			ip, e := netip.ParseAddr(item)
			if e != nil {
				return a, fmt.Errorf("invalid OWNPANEL_TRUSTED_PROXIES")
			}
			p = netip.PrefixFrom(ip, ip.BitLen())
		}
		if p.Bits() == 0 {
			return a, fmt.Errorf("trusted proxies must be restricted")
		}
		a.proxies = append(a.proxies, p)
	}
	if gateway == "" && internal == "" && file == "" {
		return a, nil
	}
	if a.gateway, err = checkedOrigin(gateway, false); err != nil {
		return a, fmt.Errorf("OWNPANEL_GATEWAY_URL: %w", err)
	}
	if internal == "" {
		internal = a.gateway
	}
	if a.internal, err = checkedOrigin(internal, true); err != nil {
		return a, fmt.Errorf("OWNPANEL_GATEWAY_INTERNAL_URL: %w", err)
	}
	f, err := os.Open(file)
	if err != nil {
		return a, fmt.Errorf("open gateway service token: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 || info.Mode().Perm()&0077 != 0 {
		return a, fmt.Errorf("gateway token file must be private and at most 4 KiB")
	}
	b, err := io.ReadAll(io.LimitReader(f, 4097))
	a.credential = strings.TrimSpace(string(b))
	if err != nil || len(b) > 4096 || a.credential == "" || strings.ContainsAny(a.credential, " \r\n\t") {
		return a, fmt.Errorf("invalid gateway token file")
	}
	a.client = &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 8, IdleConnTimeout: time.Minute}, Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return a, nil
}

func (a publicAccess) trusted(r *http.Request) bool {
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	if err == nil {
		for _, p := range a.proxies {
			if p.Contains(peer.Addr().Unmap()) {
				return true
			}
		}
	}
	return false
}

func (a publicAccess) originOK(r *http.Request) bool {
	if a.origin == "" {
		return sameRequestOrigin(r)
	}
	return r.Header.Get("Origin") == "" || r.Header.Get("Origin") == a.origin
}

func (a publicAccess) source(r *http.Request) string {
	if a.trusted(r) {
		if ip, err := netip.ParseAddr(r.Header.Get("X-Real-IP")); err == nil {
			return ip.Unmap().String()
		}
	}
	return loginSource(r)
}

func (a publicAccess) call(ctx context.Context, method, path string, body []byte, out any) bool {
	req, err := http.NewRequestWithContext(ctx, method, a.internal+path, bytes.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Service "+a.credential)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := a.client.Do(req)
	if err != nil {
		return false
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return false
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 64<<10+1))
	return err == nil && len(b) <= 64<<10 && json.Unmarshal(b, out) == nil
}

func (a publicAccess) allowed(ctx context.Context, id int64) bool {
	var result struct {
		UserID     int64 `json:"user_id"`
		Permission string
		Allowed    bool
	}
	return id > 0 && a.call(ctx, "GET", "/internal/auth/users/"+strconv.FormatInt(id, 10)+"/permissions/"+gatewayPermission, nil, &result) && result.UserID == id && result.Permission == gatewayPermission && result.Allowed
}

func loginChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return hex.EncodeToString(sum[:])
}

func (a publicAccess) register(mux *http.ServeMux, issue func(http.ResponseWriter, *http.Request, int64, string)) {
	mux.HandleFunc("GET /auth/start", func(w http.ResponseWriter, r *http.Request) {
		v := token()
		http.SetCookie(w, &http.Cookie{Name: challengeCookie, Value: v, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 600})
		http.Redirect(w, r, a.gateway+"/entry/"+gatewayApp+"?login_challenge="+loginChallenge(v), http.StatusSeeOther)
	})
	mux.HandleFunc("GET /auth/callback", func(w http.ResponseWriter, r *http.Request) {
		c, e := r.Cookie(challengeCookie)
		if e != nil {
			http.Redirect(w, r, "/auth/start", http.StatusSeeOther)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: challengeCookie, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
		code := r.URL.Query().Get("code")
		if len(c.Value) != 64 || !grantCode.MatchString(code) {
			fail(w, 403, fmt.Errorf("登录票据无效"))
			return
		}
		var grant struct {
			Aud, Name string
			ID        int64  `json:"user_id"`
			Challenge string `json:"login_challenge"`
		}
		body, _ := json.Marshal(map[string]string{"code": code})
		if !a.call(r.Context(), "POST", "/internal/auth/exchange", body, &grant) || grant.Aud != gatewayApp || subtle.ConstantTimeCompare([]byte(grant.Challenge), []byte(loginChallenge(c.Value))) != 1 || !a.allowed(r.Context(), grant.ID) {
			fail(w, 403, fmt.Errorf("网关授权无效，请重新登录"))
			return
		}
		issue(w, r, grant.ID, grant.Name)
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})
}
