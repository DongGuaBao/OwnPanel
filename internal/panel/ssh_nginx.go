package panel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const sshNginxProgram = `
import os,sys,json,subprocess,pathlib,hashlib,shutil,tempfile,time,fcntl,datetime,re
P=pathlib.Path('/etc/nginx/ownpanel'); S=P/'sites'
names=['server.conf','routing.conf','tls.conf','custom.conf']
def cmd(*args):
 p=subprocess.run(args,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True,timeout=15)
 if p.returncode: raise RuntimeError(p.stdout[-12000:])
 return p.stdout
def running():return subprocess.run(['systemctl','is-active','--quiet','nginx']).returncode==0
def revision(d):return hashlib.sha256('\x00'.join((d/n).read_text() for n in names).encode()).hexdigest()
def reload():
 cmd('nginx','-t')
 if running():cmd('nginx','-s','reload')
def setup():
 for d in [S,P/'logs',P/'backups',P/'challenges']:d.mkdir(parents=True,exist_ok=True)
 include=pathlib.Path('/etc/nginx/conf.d/ownpanel.conf')
 base='include "'+str(S)+'/*/server.conf";\n'
 upgrade="map $http_upgrade $connection_upgrade { default upgrade; '' close; }\n"
 original=include.read_text() if include.exists() else None
 if original is not None and original not in (base,upgrade+base):raise RuntimeError('Existing ownpanel.conf is not owned by this panel')
 if original==upgrade+base:return
 out=cmd('nginx','-T')
 text=base if re.search(r'map\s+\$http_upgrade\s+\$connection_upgrade\s*\{',out) else upgrade+base
 if original!=text:
  include.write_text(text)
  try:
   out=cmd('nginx','-T')
   if '# configuration file '+str(include)+':' not in out:raise RuntimeError('Nginx does not include /etc/nginx/conf.d/*.conf')
  except:
   if original is None:include.unlink(missing_ok=True)
   else:include.write_text(original)
   raise

def records():
 result=[]
 for d in sorted(S.glob('*')):
  if not d.is_dir() or not (d/'site.json').exists():continue
  site=json.loads((d/'site.json').read_text());files={n:(d/n).read_text() for n in names}
  for binding in site.get('bindings',[]):
   cert=d/('certificate-'+binding['id']+'.pem')
   if cert.exists():binding['certificateExpiry']=cmd('openssl','x509','-in',str(cert),'-noout','-enddate').strip().split('=',1)[-1]
  public_certificates={}
  for name in ['certificate.pem']+['certificate-'+binding['id']+'.pem' for binding in site.get('bindings',[])]:
   candidate=d/name
   if not candidate.exists() and name=='certificate-default.pem':candidate=d/'certificate.pem'
   if candidate.exists():public_certificates[name]=candidate.read_text()
  result.append({'site':site,'files':files,'revision':revision(d),'certificateExpiry':'','publicCertificates':public_certificates})
 return {'sites':result,'running':running(),'prefix':str(P)}
def version_site(directory,site_id):
 if directory.is_symlink() or not directory.is_dir():return False
 try:return json.loads((directory/'site.json').read_text())['id']==site_id and all((directory/n).is_file() for n in names)
 except (OSError,ValueError,KeyError):return False
a=payload['action']
if a=='list':result=records()
elif a=='versions':
 site_id=payload['id']
 versions=[{'id':d.name,'time':datetime.datetime.fromtimestamp(d.stat().st_mtime,datetime.timezone.utc).isoformat()} for d in (P/'backups').glob(site_id+'-*') if version_site(d,site_id)]
 result={'versions':sorted(versions,key=lambda entry:entry['time'],reverse=True)}
elif a=='control':
 operation=payload['operation']
 if operation=='test':out=cmd('nginx','-t')
 elif operation=='reload':out=cmd('nginx','-t');out+=cmd('nginx','-s','reload')
 elif operation in ('start','stop'):out=cmd('systemctl',operation,'nginx')
 else:raise RuntimeError('Invalid operation')
 result={'output':out}
elif a=='logs':
 f=P/'logs'/(payload['id']+'.'+payload['kind']+'.log')
 if f.exists():
  with f.open('rb') as stream:
   stream.seek(max(0,f.stat().st_size-262144));text=stream.read().decode('utf-8',errors='replace')
  result={'text':'\n'.join(text.splitlines()[-300:])}
 else:result={'text':''}
else:
 P.mkdir(parents=True,exist_ok=True)
 lock=(P/'.lock').open('a');fcntl.flock(lock,fcntl.LOCK_EX)
 setup();d=S/payload['id']
 if a in ('save','raw','restore'):
  exists=d.exists()
  expected=payload.get('revision','')
  if a!='restore' and ((exists and revision(d)!=expected) or (not exists and expected)):raise RuntimeError('Configuration changed; refresh before retrying')
  stage=pathlib.Path(tempfile.mkdtemp(prefix='stage-',dir=P))
  try:
   if a=='restore':
    source=P/'backups'/payload['version']
    if not version_site(source,payload['id']):raise RuntimeError('No complete snapshot for this site')
    for file in source.iterdir():
     if file.is_symlink() or not file.is_file():raise RuntimeError('Snapshot contains non-regular files')
     shutil.copy2(file,stage/file.name)
   elif a=='raw':
    shutil.copytree(d,stage,dirs_exist_ok=True);(stage/payload['file']).write_text(payload['content'])
    metadata=json.loads((stage/'site.json').read_text());metadata['manual']=True;(stage/'site.json').write_text(json.dumps(metadata))
   else:
    for n,v in payload['files'].items():(stage/n).write_text(v)
    for pair in payload.get('certificateNames',[['certificate.pem','private.key','']]):
     for n in pair[:2]:
      if not (stage/n).exists():
       source=d/n
       if not source.exists() and n in ('certificate-default.pem','private-default.key'):source=d/('certificate.pem' if n.startswith('certificate') else 'private.key')
       if source.exists():shutil.copy2(source,stage/n)
     if payload['tls'] and not all((stage/n).exists() for n in pair[:2]):raise RuntimeError('HTTPS requires certificate and private key for '+pair[2])
    for key in stage.glob('private*.key'):os.chmod(key,0o600)
   if (stage/'private.key').exists():os.chmod(stage/'private.key',0o600)
   backup=P/'backups'/(payload['id']+'-'+str(time.time_ns()))
   if exists:d.rename(backup)
   try:
    stage.rename(d);reload()
   except:
    if d.exists():shutil.rmtree(d)
    if exists:backup.rename(d)
    try:reload()
    except:pass
    raise
  finally:
   if stage.exists():shutil.rmtree(stage)
  result={'ok':True}
 elif a=='delete':
  backup=P/'backups'/(payload['id']+'-deleted-'+str(time.time_ns()));d.rename(backup)
  try:reload()
  except:
   backup.rename(d)
   try:reload()
   except:pass
   raise
  result={'ok':True}
 else:raise RuntimeError('Unsupported operation')
print(json.dumps(result))
`

func (d *sshDriver) nginx(w http.ResponseWriter, r *http.Request, ctx context.Context) {
	d.nginxMu.Lock()
	defer d.nginxMu.Unlock()
	p := strings.TrimPrefix(r.URL.Path, "/api/nginx/")
	body := map[string]any{}
	switch {
	case p == "sites" && r.Method == "GET":
		body["action"] = "list"
	case p == "sites" && r.Method == "POST":
		var b struct {
			PatchOnly bool   `json:"patchOnly"`
			Site      site   `json:"site"`
			Revision  string `json:"revision"`
		}
		if e := decode(w, r, &b); e != nil {
			fail(w, 400, e)
			return
		}
		if err := normalizeSiteBindings(&b.Site); err != nil {
			fail(w, 400, err)
			return
		}
		if e := validateRemoteSite(b.Site, "/"); e != nil {
			fail(w, 400, e)
			return
		}
		b.Site.Manual = false
		generated := renderSiteAt(b.Site, "/etc/nginx/ownpanel")
		if b.Revision != "" {
			raw, err := d.run(ctx, d.n.Sudo || d.n.User == "root", `python3 - "$1" <<'OWNPANEL_SNAPSHOT'
import json,pathlib,sys
p=pathlib.Path(sys.argv[1])
print(json.dumps({'site':json.loads((p/'site.json').read_text()),'files':{n:(p/n).read_text() for n in ['server.conf','routing.conf','tls.conf','custom.conf']}}))
OWNPANEL_SNAPSHOT`, "/etc/nginx/ownpanel/sites/"+b.Site.ID)
			if err != nil {
				fail(w, 400, err)
				return
			}
			var previous struct {
				Site  site              `json:"site"`
				Files map[string]string `json:"files"`
			}
			if err = json.Unmarshal([]byte(raw), &previous); err != nil {
				fail(w, 400, err)
				return
			}
			if err = patchSiteFiles(previous.Site, "/etc/nginx/ownpanel", previous.Files, generated); err != nil {
				fail(w, 409, err)
				return
			}
		}
		files := map[string]string{}
		for k, v := range generated {
			files[k] = string(v)
		}

		if b.Site.QUIC {
			version, err := d.run(ctx, d.n.Sudo || d.n.User == "root", "nginx -V 2>&1")
			if err != nil || !strings.Contains(version, "--with-http_v3_module") {
				fail(w, 400, fmt.Errorf("目标 Nginx 未编译 HTTP/3 模块，无法启用 QUIC"))
				return
			}
		}
		if b.Site.TLS {
			for _, pair := range siteCertificateNames(b.Site) {
				cert := files[pair[0]]
				key := files[pair[1]]
				if cert == "" {
					public, err := d.run(ctx, true, `if test -f "$1"; then cat -- "$1"; else cat -- "$2"; fi`, "/etc/nginx/ownpanel/sites/"+b.Site.ID+"/"+pair[0], "/etc/nginx/ownpanel/sites/"+b.Site.ID+"/"+legacyCertificateName(pair[0]))
					if err != nil {
						fail(w, 400, fmt.Errorf("域名 %s 缺少证书", pair[2]))
						return
					}
					cert = public
				}
				if err := validateCertificateData([]byte(cert), []byte(key), pair[2]); err != nil {
					fail(w, 400, err)
					return
				}
			}
		}
		body = map[string]any{"action": "save", "id": b.Site.ID, "files": files, "tls": b.Site.TLS, "certificateNames": siteCertificateNames(b.Site), "revision": b.Revision}
		if b.Site.Kind == "static" {
			if _, e := d.run(ctx, true, `set -eu; test -d "$1"`, b.Site.Root); e != nil {
				fail(w, 400, fmt.Errorf("静态目录不存在或超出文件根目录"))
				return
			}
		}
	case p == "control" && r.Method == "POST":
		var b struct {
			Action string `json:"action"`
		}
		if e := decode(w, r, &b); e != nil {
			fail(w, 400, e)
			return
		}
		body = map[string]any{"action": "control", "operation": b.Action}
	case p == "logs" && r.Method == "GET":
		id, kind := r.URL.Query().Get("id"), r.URL.Query().Get("kind")
		if !validName(id) || (kind != "access" && kind != "error") {
			fail(w, 400, fmt.Errorf("日志参数无效"))
			return
		}
		body = map[string]any{"action": "logs", "id": id, "kind": kind}
	case strings.HasPrefix(p, "sites/"):
		parts := strings.Split(p, "/")
		id := parts[1]
		if !validName(id) {
			fail(w, 400, fmt.Errorf("站点 ID 无效"))
			return
		}
		if len(parts) == 2 && r.Method == "DELETE" {
			body = map[string]any{"action": "delete", "id": id}
		} else if len(parts) == 3 && parts[2] == "versions" && r.Method == http.MethodGet {
			body = map[string]any{"action": "versions", "id": id}
		} else if len(parts) == 5 && parts[2] == "versions" && parts[4] == "restore" && r.Method == http.MethodPost {
			suffix, matched := strings.CutPrefix(parts[3], id+"-")
			if !matched || !validName(suffix) {
				fail(w, 400, fmt.Errorf("配置版本无效"))
				return
			}
			body = map[string]any{"action": "restore", "id": id, "version": parts[3]}
		} else if len(parts) == 3 && parts[2] == "config" && r.Method == "POST" {
			var b struct {
				File     string `json:"file"`
				Content  string `json:"content"`
				Revision string `json:"revision"`
			}
			if e := decode(w, r, &b); e != nil {
				fail(w, 400, e)
				return
			}
			if b.File != "server.conf" && b.File != "routing.conf" && b.File != "tls.conf" && b.File != "custom.conf" {
				fail(w, 400, fmt.Errorf("配置文件无效"))
				return
			}
			if b.File == "server.conf" {
				v, e := replaceMarkedBlocks(b.Content, certStart, certEnd, ` location ^~ /.well-known/acme-challenge/ { root "/etc/nginx/ownpanel/challenges"; }`)
				if e != nil {
					fail(w, 400, e)
					return
				}
				b.Content = v
			}
			body = map[string]any{"action": "raw", "id": id, "file": b.File, "content": b.Content, "revision": b.Revision}
		} else {
			fail(w, 405, fmt.Errorf("不支持该操作"))
			return
		}
	default:
		fail(w, 501, fmt.Errorf("SSH 模式不支持该 Nginx 操作"))
		return
	}
	data, _ := json.Marshal(body)
	program := "import json,base64\npayload=json.loads(base64.b64decode('" + base64.StdEncoding.EncodeToString(data) + "'))\n" + sshNginxProgram
	script := "exec python3 - <<'OWNPANEL_PYTHON'\n" + program + "\nOWNPANEL_PYTHON\n"
	out, e := d.run(ctx, d.n.Sudo || d.n.User == "root", script)
	if e != nil {
		code := 400
		if strings.Contains(e.Error(), "Configuration changed") {
			code = 409
		}
		fail(w, code, e)
		return
	}
	var result any
	if e = json.Unmarshal([]byte(out), &result); e != nil {
		fail(w, 502, fmt.Errorf("远端 Nginx 响应无效"))
		return
	}
	if body["action"] == "list" {
		if inventory, ok := result.(map[string]any); ok {
			if items, ok := inventory["sites"].([]any); ok {
				for _, item := range items {
					entry, ok := item.(map[string]any)
					if !ok {
						continue
					}
					raw, _ := json.Marshal(entry["site"])
					var saved site
					if err := json.Unmarshal(raw, &saved); err != nil {
						fail(w, 502, fmt.Errorf("远端站点记录无效"))
						return
					}
					public, _ := entry["publicCertificates"].(map[string]any)
					refreshSiteCertificateInfo(&saved, func(name string) []byte { text, _ := public[name].(string); return []byte(text) })
					entry["site"] = saved
					delete(entry, "publicCertificates")
				}
			}
		}
	}
	respond(w, result)
}
