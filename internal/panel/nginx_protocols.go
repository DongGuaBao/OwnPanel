package panel

import "fmt"

// Missing fields retain the old single-port behavior without silently changing
// the listeners of an existing site when the Agent is upgraded.
func siteProtocols(s site) (httpEnabled, httpsEnabled bool, tlsPort int) {
	httpEnabled = !s.TLS
	if s.HTTPEnabled != nil {
		httpEnabled = *s.HTTPEnabled
	}
	httpsEnabled = s.TLS
	if s.HTTPSEnabled != nil {
		httpsEnabled = *s.HTTPSEnabled
	}
	tlsPort = s.TLSPort
	if s.HTTPSEnabled == nil && tlsPort == 0 {
		tlsPort = s.Port
	}
	return
}
func validateSiteProtocols(s site) error {
	httpEnabled, httpsEnabled, tlsPort := siteProtocols(s)
	valid := func(port int) bool { return port >= 1 && port <= 65535 }
	if !httpEnabled && !httpsEnabled && !s.QUIC {
		return fmt.Errorf("至少启用 HTTP、HTTPS 或 QUIC 中的一种")
	}
	if httpEnabled && !valid(s.Port) {
		return fmt.Errorf("HTTP 端口应为 1–65535")
	}
	if httpsEnabled && (!s.TLS || !valid(tlsPort)) {
		return fmt.Errorf("启用 HTTPS 需要证书及有效的 SSL 端口")
	}
	if s.QUIC && (!s.TLS || !valid(s.QUICPort)) {
		return fmt.Errorf("启用 QUIC 需要证书及有效的 UDP 端口")
	}
	if httpEnabled && httpsEnabled && s.Port == tlsPort {
		return fmt.Errorf("HTTP 与 HTTPS 不能共用同一个 TCP 端口")
	}
	if s.TLS && !httpsEnabled && !s.QUIC {
		return fmt.Errorf("请启用 HTTPS 或 QUIC 监听")
	}
	return nil
}
