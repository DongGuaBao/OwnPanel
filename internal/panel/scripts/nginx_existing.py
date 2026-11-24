"""Discover effective Nginx configuration graphs without rewriting existing sites."""
import base64,datetime,fcntl,glob,hashlib,json,os,pathlib,re,shlex,shutil,ssl,stat,subprocess,tempfile,time
ROOT=pathlib.Path(os.environ.get('OWNPANEL_NGINX_EXISTING_ROOT','/var/lib/ownpanel-nginx-existing'))

def command(args):
    p=subprocess.run(args,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True,errors='replace',timeout=25)
    return p.returncode,p.stdout
def checked(args):
    code,out=command(args)
    if code:raise ValueError(out[-16000:])
    return out
def digest(text):return hashlib.sha256(text.encode()).hexdigest()
def real(p):return str(pathlib.Path(p).resolve())
def atomic(p,text,original=None):
    p=pathlib.Path(p);fd,tmp=tempfile.mkstemp(prefix='.ownpanel-',dir=str(p.parent))
    try:
        with os.fdopen(fd,'w') as f:
            f.write(text);f.flush()
            if original:os.fchown(f.fileno(),original.st_uid,original.st_gid);os.fchmod(f.fileno(),stat.S_IMODE(original.st_mode))
            else:os.fchmod(f.fileno(),0o600)
            os.fsync(f.fileno())
        os.replace(tmp,p)
    finally:
        if os.path.exists(tmp):os.unlink(tmp)

def instances(extra=()):
    found={};binaries=set()
    def add(binary,config='',prefix='',pid=0,global_args=''):
        if not binary or not pathlib.Path(binary).is_file():return
        binary=real(binary);binaries.add(binary)
        code,version=command([binary,'-V'])
        opts=dict(re.findall(r'--([\w-]+)=(\S+)',version))
        prefix=prefix or opts.get('prefix','/usr/local/nginx')
        config=config or opts.get('conf-path',str(pathlib.Path(prefix)/'conf/nginx.conf'))
        if not os.path.isabs(config):config=os.path.join(prefix,config)
        config=real(config);prefix=real(prefix)
        if not pathlib.Path(config).is_file():return
        # A privilege-delegating wrapper and /proc/exe may identify the same
        # installation. Only canonicalize when the installed binary reports
        # exactly the same build; configuration and runtime prefix stay distinct.
        identity_binary=binary
        installed=opts.get('sbin-path',os.path.join(opts.get('prefix','/usr/local/nginx'),'sbin/nginx'))
        if not os.path.isabs(installed):installed=os.path.join(opts.get('prefix','/usr/local/nginx'),installed)
        installed=real(installed)
        if installed!=binary and pathlib.Path(installed).is_file():
            installed_code,installed_version=command([installed,'-V'])
            if code==0 and installed_code==0 and installed_version==version:identity_binary=installed
        identifier=digest(identity_binary+'\0'+config+'\0'+prefix)[:24]
        label='Nginx'
        if binary.startswith('/opt/ownpanel-nginx/'):label='OwnPanel 私有 Nginx'
        elif '/www/server/' in binary or '/www/server/' in config:label='宝塔 Nginx'
        elif binary=='/usr/sbin/nginx':label='系统 Nginx'
        item={'id':identifier,'binary':binary,'config':config,'prefix':prefix,'pid':pid,'running':bool(pid),'global':global_args,'label':label,'httpLog':opts.get('http-log-path','logs/access.log'),'errorLog':opts.get('error-log-path','logs/error.log')}
        previous=found.get(identifier)
        if previous and previous['running'] and not pid:
            # Keep the live PID/identity, but use the wrapper for management.
            if binary!=identity_binary:previous['controlBinary']=binary
            return
        found[identifier]=item
    for entry in pathlib.Path('/proc').glob('[0-9]*'):
        try:
            raw=(entry/'cmdline').read_bytes().replace(b'\x00',b' ').decode(errors='replace').strip()
            if not raw.startswith('nginx: master process '):continue
            exe=os.readlink(entry/'exe');text=raw[len('nginx: master process '):];args=shlex.split(text)
            def option(name):
                try:return args[args.index(name)+1]
                except (ValueError,IndexError):return ''
            g=re.search(r'(?:^|\s)-g\s+(.+?)(?=\s+-[cp]\s|$)',text)
            global_args=g.group(1).strip() if g else ''
            if global_args[:1] in ('"',"'") and global_args[-1:]==global_args[:1]:global_args=global_args[1:-1]
            add(exe,option('-c'),option('-p'),int(entry.name),global_args)
        except (OSError,ValueError,subprocess.TimeoutExpired):continue
    for binary in ('/usr/sbin/nginx',shutil.which('nginx'),'/www/server/nginx/sbin/nginx','/usr/local/nginx/sbin/nginx'):
        if binary:
            try:add(binary)
            except (OSError,subprocess.TimeoutExpired):pass
    for item in extra:
        binary=shutil.which(item.get('binary') or 'nginx')
        if binary:
            try:add(binary,item.get('config',''),item.get('prefix',''))
            except (OSError,subprocess.TimeoutExpired):pass
    return sorted(found.values(),key=lambda i:(not i['running'],i['config']))
def invocation(instance,operation):
    args=[instance.get('controlBinary',instance['binary']),'-p',instance['prefix']+'/', '-c',instance['config']]
    if instance.get('global'):args+=['-g',instance['global']]
    return args+operation

def lua_end(text,start):
    depth=1;i=start
    while i<len(text):
        if text[i:i+2]=='--':
            i+=2
            long=re.match(r'\[(=*)\[',text[i:])
            if not long:
                end=text.find('\n',i);i=len(text) if end<0 else end;continue
        else:long=re.match(r'\[(=*)\[',text[i:])
        if long:
            close=']'+long.group(1)+']';end=text.find(close,i+long.end())
            if end<0:raise ValueError('未闭合的 Lua 长字符串或注释')
            i=end+len(close);continue
        c=text[i]
        if c in ('"',"'"):
            quote=c;i+=1
            while i<len(text):
                if text[i]=='\\':i+=2;continue
                if text[i]==quote:i+=1;break
                i+=1
            continue
        if c=='{':depth+=1
        elif c=='}':
            depth-=1
            if depth==0:return i+1
        i+=1
    raise ValueError('未闭合的 Lua 配置块')

def lex(text):
    """Nginx tokens with exact source offsets; comments and quoted braces survive."""
    result=[];word='';start=None;quote='';i=0;line=1;start_line=1
    def flush(end):
        nonlocal word,start
        if start is not None:result.append((word,start,end,start_line,False));word='';start=None
    while i<len(text):
        c=text[i]
        if c=='\\' and i+1<len(text):
            if start is None:start=i;start_line=line
            following=text[i+1]
            word+=({'n':'\n','r':'\r','t':'\t'}.get(following,following) if following in 'nrt' else following if following in '\\"\' ;{}#\t\n' else '\\'+following)
            line+=following=='\n';i+=2;continue
        if quote:
            if c==quote:quote=''
            else:word+=c
            line+=c=='\n';i+=1;continue
        if c in ('"',"'"):
            if start is None:start=i;start_line=line
            quote=c;i+=1;continue
        if c=='#':
            flush(i)
            while i<len(text) and text[i]!='\n':i+=1
            continue
        if c.isspace():flush(i);line+=c=='\n';i+=1;continue
        if c=='$' and i+1<len(text) and text[i+1]=='{':
            if start is None:start=i;start_line=line
            end=text.find('}',i+2)
            if end<0:raise ValueError('未闭合的变量，行 '+str(line))
            word+=text[i:end+1];i=end+1;continue
        if c in ';{}':
            flush(i);lua=c=='{' and bool(result) and result[-1][0].endswith('_by_lua_block')
            result.append((c,i,i+1,line,True));i+=1
            if lua:
                end=lua_end(text,i);line+=text[i:end].count('\n');result.append(('}',end-1,end,line,True));i=end
            continue
        if start is None:start=i;start_line=line
        word+=c;i+=1
    flush(i)
    if quote:raise ValueError('未闭合的引号，行 '+str(line))
    return result
def parse(text,source):
    tokens=lex(text);at=0
    def block(nested=False):
        nonlocal at
        nodes=[]
        while at<len(tokens):
            if tokens[at][4] and tokens[at][0]=='}':
                if not nested:raise ValueError('多余的右括号')
                end=tokens[at][2];at+=1;return nodes,end
            first=tokens[at];args=[]
            while at<len(tokens) and not tokens[at][4]:args.append(tokens[at][0]);at+=1
            if not args or at>=len(tokens) or tokens[at][0]=='}':raise ValueError('指令未结束，行 '+str(first[3]))
            ending=tokens[at];at+=1;children=[];end=ending[2]
            if ending[0]=='{':children,end=block(True)
            nodes.append({'name':args[0],'args':args[1:],'children':children,'block':ending[0]=='{','file':source,'line':first[3],'start':first[1],'end':end})
        if nested:raise ValueError('未闭合的配置块')
        return nodes,len(text)
    return block()[0]

class Graph:
    def __init__(self,instance):
        self.instance=instance;self.files={};self.errors=[];self.directories=set();self.trees={}
        code,dump=command(invocation(instance,['-T']))
        self.testError=dump[-10000:] if code else ''
        self.dumpPaths=[real(p) for p in re.findall(r'^# configuration file (.+):\s*$',dump,re.M)]
        self.read(instance['config'])
    def read(self,p):
        p=real(p)
        if p in self.trees:return self.trees[p]
        if p in self.files:return []
        try:text=pathlib.Path(p).read_text()
        except (OSError,UnicodeError) as e:self.errors.append(p+': '+str(e));return []
        self.files[p]={'path':p,'revision':digest(text),'size':len(text.encode())};self.directories.add(str(pathlib.Path(p).parent))
        try:nodes=parse(text,p)
        except ValueError as e:self.errors.append(p+': '+str(e));self.trees[p]=[];return []
        self.trees[p]=nodes
        def includes(items):
            for n in items:
                if n['name']=='include':
                    n['included']=[]
                    for pattern in n['args']:
                        pattern=pattern if os.path.isabs(pattern) else os.path.join(str(pathlib.Path(self.instance['config']).parent),pattern)
                        static=re.split(r'[?*\[]',pattern,1)[0];self.directories.add(str(pathlib.Path(static).parent))
                        matches=glob.glob(pattern)
                        # nginx -T is authoritative for a relative include loaded
                        # under a different configured prefix.
                        if not matches:
                            alternate=os.path.join(self.instance['prefix'],n['args'][0])
                            matches=[q for q in self.dumpPaths if pathlib.PurePath(q).match(alternate)]
                        for q in sorted(matches):
                            if pathlib.Path(q).is_file():n['included'].append(real(q));self.read(q)
                includes(n['children'])
        includes(nodes);return nodes
    def expanded(self,nodes,seen=()):
        result=[]
        for n in nodes:
            if n['name']=='include':
                for p in n.get('included',[]):
                    if p in seen:self.errors.append('循环 include: '+p);continue
                    result+=self.expanded(self.trees.get(p,[]),seen+(p,))
            else:result.append(n)
        return result
    def sites(self,excluded):
        found=[]
        def values(nodes,name):return [n['args'] for n in self.expanded(nodes) if n['name']==name]
        def path_arg(value):return value if os.path.isabs(value) else os.path.join(self.instance['prefix'],value)
        def walk(nodes,parents=(),inherited=None):
            inherited=dict(inherited or {})
            direct=self.expanded(nodes)
            for name in ('root','access_log','error_log','ssl_certificate','ssl_certificate_key'):
                v=values(direct,name)
                if v:inherited[name]=v
            root_source=next((n for n in direct if n['name']=='root'),None)
            if root_source:inherited['root-source']=root_source
            for node in direct:
                if node['name']=='server' and node['block'] and ('http' in parents or 'stream' in parents):
                    if any(node['file'].startswith(p.rstrip('/')+'/') for p in excluded):continue
                    directives=self.expanded(node['children']);domains=values(directives,'server_name');listen=values(directives,'listen');roots=values(directives,'root') or inherited.get('root',[])
                    routes=[]
                    def locations(items,location='server',inherited_root=None):
                        local_root=next((n for n in self.expanded(items) if n['name']=='root'),inherited_root)
                        for n in self.expanded(items):
                            if n['name'] in ('proxy_pass','fastcgi_pass','uwsgi_pass','grpc_pass','scgi_pass','root','alias','return','allow','deny','try_files','index','rewrite','content_by_lua_block'):routes.append({'location':location,'directive':n['name'],'value':' '.join(n['args']),'file':n['file'],'line':n['line']})
                            if n['name']=='location':
                                name=' '.join(n['args'])
                                if location!='server':name=location+' › '+name
                                direct_names={child['name'] for child in self.expanded(n['children'])}
                                routes.append({'location':name,'directive':'location','value':'','file':n['file'],'line':n['line']})
                                if local_root and not direct_names.intersection({'root','alias','proxy_pass','fastcgi_pass','uwsgi_pass','grpc_pass','scgi_pass','return','content_by_lua_block'}):
                                    routes.append({'location':name,'directive':'root (继承)','value':' '.join(local_root['args']),'file':local_root['file'],'line':local_root['line']})
                                locations(n['children'],name,local_root)
                            elif n['children']:locations(n['children'],location,local_root)
                    locations(directives,inherited_root=inherited.get('root-source'))
                    certs=[]
                    public=values(directives,'ssl_certificate') or inherited.get('ssl_certificate',[]);private=values(directives,'ssl_certificate_key') or inherited.get('ssl_certificate_key',[])
                    for index,args in enumerate(public):
                        cert_arg=lambda value:value if os.path.isabs(value) else os.path.join(str(pathlib.Path(self.instance['config']).parent),value)
                        cert={'path':cert_arg(args[0]),'keyPath':cert_arg(private[index][0]) if index<len(private) else '','expires':''}
                        try:cert['expires']=ssl._ssl._test_decode_cert(cert['path']).get('notAfter','')
                        except (OSError,ValueError,ssl.SSLError):pass
                        certs.append(cert)
                    logs={}
                    for name in ('access_log','error_log'):
                        args=values(directives,name) or inherited.get(name,[])
                        if not args:args=[[self.instance['httpLog' if name=='access_log' else 'errorLog']]]
                        logs[name]=[path_arg(a[0]) for a in args if a and a[0]!='off' and not a[0].startswith('syslog:') and '$' not in a[0]]
                    proxy=[r['value'] for r in routes if r['directive'] in ('proxy_pass','grpc_pass','uwsgi_pass')];kind='stream' if 'stream' in parents else 'mixed' if proxy and roots else 'proxy' if proxy else 'fastcgi' if any(r['directive']=='fastcgi_pass' for r in routes) else 'custom' if any(r['directive']=='content_by_lua_block' for r in routes) else 'static'
                    found.append({'id':digest(self.instance['id']+'\0'+node['file']+'\0'+str(node['start']))[:24],'instance':self.instance['id'],'file':node['file'],'line':node['line'],'domains':[d for group in domains for d in group] or ['_'],'listen':[' '.join(a) for a in listen] or ['80'],'root':roots[0][0] if roots else '', 'kind':kind,'upstreams':proxy,'routes':routes,'certificates':certs,'tls':bool(certs) or any('ssl' in a for a in listen),'logs':logs,'running':self.instance['running'],'instanceConfig':self.instance['config'],'label':self.instance['label']})
                elif node['children']:walk(node['children'],parents+(node['name'],),inherited)
        walk(self.trees.get(self.instance['config'],[]))
        return found

def inventory(request):
    result={'sites':[],'instances':[],'warnings':[]}
    for instance in instances(request.get('extra',[])):
        try:
            graph=Graph(instance);instance['files']=list(graph.files.values());instance['errors']=list(dict.fromkeys(graph.errors));instance['testError']=graph.testError
            result['sites']+=graph.sites(request.get('excluded',[]));result['instances'].append(instance)
            result['warnings']+=instance['errors']
            if instance['testError']:result['warnings'].append(instance['config']+': '+instance['testError'])
        except Exception as e:result['warnings'].append(instance['config']+': '+str(e))
    result['scannedAt']=datetime.datetime.now(datetime.timezone.utc).isoformat()
    return result

def member(request,data):
    instance=next((i for i in data['instances'] if i['id']==request.get('instance')),None)
    if not instance:raise ValueError('Nginx 实例已变化，请重新扫描')
    target=real(request.get('path',''))
    if target not in [f['path'] for f in instance['files']]:raise ValueError('配置文件不在此实例的 include 清单中，请重新扫描')
    return instance,target
def history(path):return ROOT/'history'/digest(path)
def versions(path):
    result=[]
    for d in history(path).glob('*'):
        try:
            meta=json.loads((d/'meta.json').read_text())
            if meta['path']==path and meta.get('state')=='applied':result.append({'id':d.name,'time':meta['time'],'instanceConfig':meta['instanceConfig']})
        except (OSError,ValueError,KeyError):pass
    return sorted(result,key=lambda v:(v['time'],v['id']),reverse=True)
def save(request,data,instance,path,content):
    current=pathlib.Path(path).read_text()
    if request.get('revision')!=digest(current):return {'status':409,'error':'配置已变化，请重新读取后编辑'}
    if '\0' in content:raise ValueError('配置不能包含 NUL 字符')
    attributes=os.stat(path);identifier=time.strftime('%Y%m%d-%H%M%S')+'-'+os.urandom(6).hex();d=history(path)/identifier;d.mkdir(parents=True,mode=0o700)
    (d/'before.conf').write_text(current);(d/'before.conf').chmod(0o600)
    meta={'path':path,'time':datetime.datetime.now(datetime.timezone.utc).isoformat(),'instanceConfig':instance['config'],'state':'pending'}
    atomic(d/'meta.json',json.dumps(meta))
    affected=[i for i in data['instances'] if path in [f['path'] for f in i['files']]]
    atomic(path,content,attributes)
    try:
        for i in affected:checked(invocation(i,['-t']))
        for i in affected:
            if i['running']:checked(invocation(i,['-s','reload']))
    except Exception as e:
        atomic(path,current,attributes);failures=[]
        for i in affected:
            if i['running']:
                try:checked(invocation(i,['-t']));checked(invocation(i,['-s','reload']))
                except Exception as failed:failures.append(str(failed))
        meta['state']='rejected';atomic(d/'meta.json',json.dumps(meta))
        raise ValueError('检查或重载失败，原文件已恢复：'+str(e)+('; 恢复重载失败：'+'; '.join(failures) if failures else ''))
    meta['state']='applied';atomic(d/'meta.json',json.dumps(meta));return {'ok':True,'revision':digest(content),'version':identifier,'reloaded':sum(i['running'] for i in affected)}

def execute(request):
    data=inventory(request);action=request.get('action','inventory')
    if action=='inventory':return data
    if action=='control':
        instance=next((i for i in data['instances'] if i['id']==request.get('instance')),None)
        if not instance:raise ValueError('Nginx 实例已变化')
        operation=request.get('operation')
        if operation not in ('test','reload'):raise ValueError('不支持的实例操作')
        output=checked(invocation(instance,['-t']))
        if operation=='reload':
            if not instance['running']:raise ValueError('该实例未运行')
            output+=checked(invocation(instance,['-s','reload']))
        return {'ok':True,'output':output}
    if action=='logs':
        site=next((s for s in data['sites'] if s['id']==request.get('site') and s['instance']==request.get('instance')),None)
        if not site:raise ValueError('站点已变化，请重新扫描')
        kind=request.get('kind','access_log');paths=site['logs'].get(kind,[]);p=request.get('path') or (paths[0] if paths else '')
        if p not in paths:raise ValueError('站点未配置此静态文件日志')
        if not stat.S_ISREG(os.stat(p).st_mode):raise ValueError('该日志不是普通文件')
        with open(p,'rb') as f:f.seek(0,2);size=f.tell();f.seek(max(0,size-(2<<20)));text=f.read().decode(errors='replace')
        return {'text':'\n'.join(text.splitlines()[-300:]),'path':p}
    instance,path=member(request,data)
    if action=='read':
        text=pathlib.Path(path).read_text();return {'path':path,'content':text,'revision':digest(text),'versions':versions(path)}
    if action=='save':return save(request,data,instance,path,request['content'])
    if action=='restore':
        identifier=request.get('version','')
        if not re.fullmatch(r'\d{8}-\d{6}-[a-f0-9]{12}',identifier):raise ValueError('历史版本无效')
        meta=json.loads((history(path)/identifier/'meta.json').read_text())
        if meta.get('path')!=path or meta.get('state')!='applied':raise ValueError('历史版本不属于此文件')
        return save(request,data,instance,path,(history(path)/identifier/'before.conf').read_text())
    raise ValueError('不支持的 Nginx 操作')

def main(request):
    os.umask(0o077);ROOT.mkdir(parents=True,exist_ok=True,mode=0o700)
    with (ROOT/'lock').open('a') as lock:
        fcntl.flock(lock,fcntl.LOCK_EX)
        try:result=execute(request)
        except Exception as e:result={'status':400,'error':str(e)}
    print(json.dumps(result,ensure_ascii=False))
if __name__=='__main__':main(REQUEST)
