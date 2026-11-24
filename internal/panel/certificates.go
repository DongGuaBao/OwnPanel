package panel

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

type certificateInfo struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Domains       []string  `json:"domains"`
	Subject       string    `json:"subject"`
	Issuer        string    `json:"issuer"`
	NotBefore     time.Time `json:"notBefore"`
	NotAfter      time.Time `json:"notAfter"`
	DaysRemaining int       `json:"daysRemaining"`
	Status        string    `json:"status"`
	SerialNumber  string    `json:"serialNumber"`
	Fingerprint   string    `json:"fingerprint"`
	Algorithm     string    `json:"algorithm"`
}

func inspectCertificate(chain string, now time.Time) (certificateInfo, string, error) {
	var first *x509.Certificate
	var normalized strings.Builder
	remaining := []byte(strings.TrimSpace(chain))
	for len(remaining) > 0 {
		if !bytes.HasPrefix(remaining, []byte("-----BEGIN CERTIFICATE-----")) {
			return certificateInfo{}, "", fmt.Errorf("证书链包含非证书数据")
		}
		block, rest := pem.Decode(remaining)
		if block == nil || block.Type != "CERTIFICATE" {
			return certificateInfo{}, "", fmt.Errorf("证书链必须仅包含 PEM CERTIFICATE，不能包含私钥或其他数据")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return certificateInfo{}, "", fmt.Errorf("证书解析失败：%w", err)
		}
		if first == nil {
			first = cert
		}
		normalized.Write(pem.EncodeToMemory(block))
		remaining = []byte(strings.TrimSpace(string(rest)))
	}
	if first == nil {
		return certificateInfo{}, "", fmt.Errorf("请提供有效的 PEM 证书链")
	}
	fingerprint := sha256.Sum256(first.Raw)
	domains := append([]string{}, first.DNSNames...)
	for _, ip := range first.IPAddresses {
		domains = append(domains, ip.String())
	}
	if len(domains) == 0 && first.Subject.CommonName != "" {
		domains = append(domains, first.Subject.CommonName)
	}
	status := "valid"
	if now.Before(first.NotBefore) {
		status = "notYetValid"
	} else if !now.Before(first.NotAfter) {
		status = "expired"
	} else if first.NotAfter.Sub(now) <= 30*24*time.Hour {
		status = "expiring"
	}
	return certificateInfo{Domains: domains, Subject: first.Subject.String(), Issuer: first.Issuer.String(), NotBefore: first.NotBefore, NotAfter: first.NotAfter, DaysRemaining: int(math.Ceil(first.NotAfter.Sub(now).Hours() / 24)), Status: status, SerialNumber: strings.ToUpper(first.SerialNumber.Text(16)), Fingerprint: hex.EncodeToString(fingerprint[:]), Algorithm: first.PublicKeyAlgorithm.String() + " / " + first.SignatureAlgorithm.String()}, normalized.String(), nil
}

func certificateAAD(node, id string) []byte { return []byte("ownpanel-certificate:" + node + ":" + id) }
func (m *nodeManager) certificates(w http.ResponseWriter, r *http.Request, node, id, operation string) {
	ctx := r.Context()
	if operation != "" && operation != "preview" && operation != "export" {
		fail(w, 404, fmt.Errorf("证书不存在或接口路径无效，请刷新后重试"))
		return
	}
	if operation == "preview" && id != "" || operation == "export" && id == "" {
		fail(w, 404, fmt.Errorf("证书不存在或接口路径无效，请刷新后重试"))
		return
	}
	if r.Method == "GET" && operation == "" {
		rows, err := m.db.QueryContext(ctx, "SELECT id,name,certificate FROM certificates WHERE node_id=? ORDER BY name,id", node)
		if err != nil {
			fail(w, 500, err)
			return
		}
		defer rows.Close()
		list := []certificateInfo{}
		for rows.Next() {
			var entryID, name, chain string
			if err = rows.Scan(&entryID, &name, &chain); err != nil {
				fail(w, 500, err)
				return
			}
			if id != "" && entryID != id {
				continue
			}
			info, _, err := inspectCertificate(chain, time.Now())
			if err != nil {
				fail(w, 500, err)
				return
			}
			info.ID = entryID
			info.Name = name
			list = append(list, info)
		}
		if err = rows.Err(); err != nil {
			fail(w, 500, err)
			return
		}
		if id != "" {
			if len(list) == 0 {
				fail(w, 404, fmt.Errorf("证书不存在或接口路径无效，请刷新后重试"))
				return
			}
			respond(w, list[0])
			return
		}
		respond(w, map[string]any{"certificates": list})
		return
	}
	if r.Method == "POST" && operation == "export" {
		var name, chain string
		var blob []byte
		err := m.db.QueryRowContext(ctx, "SELECT name,certificate,secret FROM certificates WHERE node_id=? AND id=?", node, id).Scan(&name, &chain, &blob)
		if err == sql.ErrNoRows {
			fail(w, 404, fmt.Errorf("证书不存在或接口路径无效，请刷新后重试"))
			return
		}
		if err != nil {
			fail(w, 500, err)
			return
		}
		size := m.aead.NonceSize()
		if len(blob) < size {
			fail(w, 500, fmt.Errorf("证书私钥数据无效"))
			return
		}
		key, err := m.aead.Open(nil, blob[:size], blob[size:], certificateAAD(node, id))
		if err != nil {
			fail(w, 500, fmt.Errorf("证书私钥解密失败"))
			return
		}
		if _, err = tls.X509KeyPair([]byte(chain), key); err != nil {
			fail(w, 500, fmt.Errorf("已保存的证书与私钥不匹配"))
			return
		}
		m.record(node, "certificate_exported", map[string]string{"id": id})
		respond(w, map[string]string{"name": name, "certificate": chain, "privateKey": string(key)})
		return
	}
	if r.Method == "DELETE" && id != "" && operation == "" {
		result, err := m.db.ExecContext(ctx, "DELETE FROM certificates WHERE node_id=? AND id=?", node, id)
		if err != nil {
			fail(w, 500, err)
			return
		}
		count, _ := result.RowsAffected()
		if count == 0 {
			fail(w, 404, fmt.Errorf("证书不存在或接口路径无效，请刷新后重试"))
			return
		}
		m.record(node, "certificate_deleted", map[string]string{"id": id})
		respond(w, map[string]bool{"ok": true})
		return
	}
	if (r.Method == "POST" && id == "" && (operation == "" || operation == "preview")) || (r.Method == "PUT" && id != "" && operation == "") {
		var body struct {
			Name        string `json:"name"`
			Certificate string `json:"certificate"`
			PrivateKey  string `json:"privateKey"`
		}
		if err := decode(w, r, &body); err != nil {
			fail(w, 400, err)
			return
		}
		info, chain, err := inspectCertificate(body.Certificate, time.Now())
		if err != nil {
			fail(w, 400, err)
			return
		}
		if _, err = tls.X509KeyPair([]byte(chain), []byte(body.PrivateKey)); err != nil {
			fail(w, 400, fmt.Errorf("证书与私钥不匹配，或私钥格式无效"))
			return
		}
		body.Name = strings.TrimSpace(body.Name)
		if operation == "preview" {
			respond(w, info)
			return
		}
		if body.Name == "" || utf8.RuneCountInString(body.Name) > 128 || strings.ContainsAny(body.Name, "\x00\r\n") {
			fail(w, 400, fmt.Errorf("请输入 1–128 字的证书名称"))
			return
		}
		updating := id != ""
		if !updating {
			id = token()[:24]
		}
		nonce := make([]byte, m.aead.NonceSize())
		if _, err = rand.Read(nonce); err != nil {
			fail(w, 500, err)
			return
		}
		blob := m.aead.Seal(nonce, nonce, []byte(body.PrivateKey), certificateAAD(node, id))
		var result sql.Result
		if updating {
			result, err = m.db.ExecContext(ctx, "UPDATE certificates SET name=?,certificate=?,secret=?,fingerprint=? WHERE node_id=? AND id=?", body.Name, chain, blob, info.Fingerprint, node, id)
		} else {
			result, err = m.db.ExecContext(ctx, "INSERT INTO certificates(node_id,id,name,certificate,secret,fingerprint) VALUES(?,?,?,?,?,?)", node, id, body.Name, chain, blob, info.Fingerprint)
		}
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint") {
				fail(w, 409, fmt.Errorf("该证书已在此主机的证书夹中，请更新已有记录"))
			} else {
				fail(w, 500, err)
			}
			return
		}
		count, _ := result.RowsAffected()
		if count == 0 {
			fail(w, 404, fmt.Errorf("证书不存在或接口路径无效，请刷新后重试"))
			return
		}
		info.ID = id
		info.Name = body.Name
		m.record(node, "certificate_saved", map[string]string{"id": id, "fingerprint": info.Fingerprint})
		respond(w, info)
		return
	}
	w.WriteHeader(405)
}
