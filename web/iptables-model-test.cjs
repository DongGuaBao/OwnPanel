const fs=require('node:fs'),assert=require('node:assert/strict'),ts=require('typescript');
const source=fs.readFileSync(__dirname+'/src/iptables-model.ts','utf8');const output=ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText;
const m={exports:{}};new Function('exports','module',output)(m.exports,m);const {tokens,parseRules,draftRule,buildRule}=m.exports;
for(const line of [
 '-A INPUT -p tcp -m tcp --dport 443 -m comment --comment "-s" -j ACCEPT',
 '-A INPUT ! -s 192.0.2.0/24 -p tcp --dport 22 -j DROP',
 '-A INPUT -m comment --comment "quoted \\"text\\" and spaces" -j ACCEPT',
 '-A INPUT -p tcp -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT',
 '-A PREROUTING -p tcp --dport 80 -j DNAT --to-destination 192.0.2.2:8080',
 '-A INPUT -m unknown --future-option "-p" -j ACCEPT',
]){
 const args=tokens(line).slice(2),draft=draftRule(args),rebuilt=tokens(buildRule(tokens(line)[1],draft)).slice(2);
 if(draft._opaque)assert.deepEqual(rebuilt,args);else {
  const after=draftRule(rebuilt);for(const key of Object.keys(draft))assert.equal(after[key],draft[key]);
 }
}
const document='*filter\n:INPUT ACCEPT [0:0]\n:CUSTOM - [0:0]\n[3:120] -A INPUT -m comment --comment "hello world" -j CUSTOM\nCOMMIT\n*nat\n:PREROUTING ACCEPT [0:0]\nCOMMIT\n';
const parsed=parseRules(document);assert.deepEqual(parsed.errors,[]);assert.equal(parsed.tables.length,2);assert.equal(parsed.tables[0].chains[0].rules[0].packets,'3');assert.equal(parsed.lines.join('\n'),document);
assert.throws(()=>tokens('"unterminated'));assert.throws(()=>buildRule('INPUT\nCOMMIT',{}));
console.log('PASS iptables model preserves unknown and negated matches, quoted values, NAT, counters and complete documents');
