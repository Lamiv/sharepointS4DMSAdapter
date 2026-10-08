// k6 workload for the SAP Content Server HTTP interface, shaped like KPro /
// ArchiveLink traffic from S/4HANA (DMS originals, GOS attachments).
// Env: CS_URL, CONTREP, VUS, DURATION
import http from 'k6/http';
import { check, sleep } from 'k6';
import { Trend } from 'k6/metrics';

const CS = __ENV.CS_URL || 'http://adapter:8090/ContentServer/ContentServer.dll';
const REP = __ENV.CONTREP || 'Z1';
const VUS = parseInt(__ENV.VUS || '10', 10);
const DURATION = __ENV.DURATION || '60s';

export const options = {
  scenarios: { kpro: { executor: 'constant-vus', vus: VUS, duration: DURATION } },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    checks: ['rate>0.99'],
    op_get: ['p(95)<1500'],
    op_info: ['p(95)<1000'],
  },
  summaryTrendStats: ['avg', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
  setupTimeout: '300s',
};

const OPS = ['info', 'get', 'get_range', 'docGet', 'create_put', 'create_post', 'create_large', 'delete'];
const trends = Object.fromEntries(OPS.map((k) => [k, new Trend(`op_${k}`, true)]));

function payload(n) {
  const b = new Uint8Array(n);
  for (let i = 0; i < n; i += 4096) b[i] = (i * 7) & 0xff;
  return b.buffer;
}
const SMALL = payload(200 * 1024);
const MEDIUM = payload(1024 * 1024);
const LARGE = payload(8 * 1024 * 1024); // > 4 MiB -> resumable upload session

const rnd = () => Math.floor(Math.random() * 1e9).toString(16);
const docId = (p) => `${p}${__VU.toString(16)}${Date.now().toString(16)}${rnd()}`.toUpperCase().padEnd(32, '0').slice(0, 32);
const url = (cmd, params) => `${CS}?${cmd}&pVersion=0047&contRep=${REP}&` + Object.entries(params).map(([k, v]) => `${k}=${encodeURIComponent(v)}`).join('&');

function multipartBody(parts) {
  const boundary = 'KoZIhvcNAQcB' + rnd();
  const enc = (s) => { const a = new Uint8Array(s.length); for (let i = 0; i < s.length; i++) a[i] = s.charCodeAt(i); return a; };
  const chunks = [];
  for (const p of parts) {
    chunks.push(enc(`--${boundary}\r\nX-compId: ${p.compId}\r\nContent-Type: ${p.type}\r\nContent-Length: ${p.data.byteLength}\r\n\r\n`));
    chunks.push(new Uint8Array(p.data));
    chunks.push(enc('\r\n'));
  }
  chunks.push(enc(`--${boundary}--\r\n`));
  const total = chunks.reduce((n, c) => n + c.length, 0);
  const out = new Uint8Array(total);
  let off = 0;
  for (const c of chunks) { out.set(c, off); off += c.length; }
  return { body: out.buffer, type: `multipart/form-data; boundary=${boundary}` };
}

export function setup() {
  const docs = [];
  for (let i = 0; i < 30; i++) {
    const id = `SEED${i.toString().padStart(28, '0')}`;
    // 403 = already exists (re-seeding when levels share one stack): expected per spec.
    const r = http.put(url('create', { docId: id, compId: 'data' }), SMALL, {
      headers: { 'Content-Type': 'application/pdf' },
      responseCallback: http.expectedStatuses(201, 403),
    });
    check(r, { 'seed 201/403': (x) => x.status === 201 || x.status === 403 });
    docs.push(id);
  }
  return { docs };
}

const mine = []; // documents created by this VU, candidates for delete
function pick(a) { return a[Math.floor(Math.random() * a.length)]; }

export default function (data) {
  const roll = Math.random() * 100;
  let r, op;
  if (roll < 25) {
    op = 'info';
    r = http.get(url('info', { docId: pick(data.docs) }));
    check(r, { 'info 200': (x) => x.status === 200 && x.headers['X-Numbercomps'] !== undefined });
  } else if (roll < 55) {
    op = 'get';
    r = http.get(url('get', { docId: pick(data.docs), compId: 'data' }), { responseType: 'none' });
    check(r, { 'get 200': (x) => x.status === 200 });
  } else if (roll < 60) {
    op = 'get_range';
    r = http.get(url('get', { docId: pick(data.docs), compId: 'data', fromOffset: '1000', toOffset: '50999' }), { responseType: 'none' });
    check(r, { 'get range 200': (x) => x.status === 200 });
  } else if (roll < 65) {
    op = 'docGet';
    r = http.get(url('docGet', { docId: pick(data.docs) }), { responseType: 'none' });
    check(r, { 'docGet 200': (x) => x.status === 200 });
  } else if (roll < 83) {
    op = 'create_put';
    const id = docId('P');
    r = http.put(url('create', { docId: id, compId: 'data' }), Math.random() < 0.8 ? SMALL : MEDIUM, { headers: { 'Content-Type': 'image/jpeg' }, responseType: 'none' });
    if (check(r, { 'create PUT 201': (x) => x.status === 201 })) mine.push(id);
  } else if (roll < 91) {
    op = 'create_post';
    const id = docId('M');
    const mp = multipartBody([{ compId: 'data', type: 'application/pdf', data: SMALL }, { compId: 'descr', type: 'application/x-alf-descr', data: payload(2048) }]);
    r = http.post(url('create', { docId: id }), mp.body, { headers: { 'Content-Type': mp.type }, responseType: 'none' });
    if (check(r, { 'create POST 201': (x) => x.status === 201 })) mine.push(id);
  } else if (roll < 94) {
    op = 'create_large';
    const id = docId('L');
    r = http.put(url('create', { docId: id, compId: 'data' }), LARGE, { headers: { 'Content-Type': 'application/pdf' }, timeout: '180s', responseType: 'none' });
    check(r, { 'create large 201': (x) => x.status === 201 });
  } else {
    op = 'delete';
    const id = mine.length ? mine.shift() : null;
    if (!id) { sleep(0.1); return; }
    r = http.get(url('delete', { docId: id }), { responseType: 'none' });
    check(r, { 'delete 200': (x) => x.status === 200 });
  }
  trends[op].add(r.timings.duration);
  sleep(0.1 + Math.random() * 0.4);
}

export function handleSummary(d) {
  const m = d.metrics;
  const v = (n, s) => (m[n] && m[n].values[s] !== undefined ? m[n].values[s] : NaN);
  const ms = (x) => (isNaN(x) ? '-' : x.toFixed(0));
  const secs = parseFloat(DURATION);
  const header = '| Clients | Requests | Req/s | Errors | ' + ['info', 'get 200K', 'get range', 'docGet', 'create PUT', 'create POST (2 comps)', 'create 8M', 'delete'].join(' | ') + ' | MB/s up / down |\n' +
    '|' + '---|'.repeat(OPS.length + 5) + '\n';
  const row = `| ${VUS} | ${v('http_reqs', 'count')} | ${v('http_reqs', 'rate').toFixed(1)} | ${(v('http_req_failed', 'rate') * 100).toFixed(2)}% | ` +
    OPS.map((k) => `${ms(v(`op_${k}`, 'med'))} / ${ms(v(`op_${k}`, 'p(95)'))}`).join(' | ') +
    ` | ${(v('data_sent', 'count') / 1048576 / secs).toFixed(1)} / ${(v('data_received', 'count') / 1048576 / secs).toFixed(1)} |\n`;
  return {
    'results/header.md': header,
    [`results/row-${VUS}.md`]: row,
    stdout: `\nVUs=${VUS} reqs=${v('http_reqs', 'count')} rps=${v('http_reqs', 'rate').toFixed(1)} errors=${(v('http_req_failed', 'rate') * 100).toFixed(2)}% checks=${(v('checks', 'rate') * 100).toFixed(2)}%\n` +
      OPS.map((k) => `  ${k.padEnd(13)} med ${ms(v(`op_${k}`, 'med')).padStart(6)} ms   p95 ${ms(v(`op_${k}`, 'p(95)')).padStart(6)} ms`).join('\n') + '\n',
  };
}
