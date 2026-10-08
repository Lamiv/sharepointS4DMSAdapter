// k6 workload approximating Fiori / DMS usage against the Document REST API.
// Env: BASE_URL, API_KEY, VUS, DURATION, REPO
import http from 'k6/http';
import { check, sleep } from 'k6';
import { Trend } from 'k6/metrics';

const BASE = __ENV.BASE_URL || 'http://adapter:8080/api/v1';
const KEY = __ENV.API_KEY || 'loadtest-key';
const REPO = __ENV.REPO || 'DMS';
const VUS = parseInt(__ENV.VUS || '10', 10);
const DURATION = __ENV.DURATION || '60s';

export const options = {
  scenarios: {
    mixed: { executor: 'constant-vus', vus: VUS, duration: DURATION },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    checks: ['rate>0.99'],
    op_download_small: ['p(95)<1500'],
    op_metadata: ['p(95)<1000'],
  },
  summaryTrendStats: ['avg', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
  setupTimeout: '300s',
};

const OPS = ['download_small', 'download_large', 'metadata', 'list', 'upload_small', 'upload_medium', 'upload_large'];
const trends = Object.fromEntries(OPS.map((k) => [k, new Trend(`op_${k}`, true)]));

function payload(n) {
  const buf = new Uint8Array(n);
  for (let i = 0; i < n; i += 4096) buf[i] = (i * 31) & 0xff;
  return buf.buffer;
}
// Per-VU buffers created once at init time.
const SMALL = payload(200 * 1024);       // photo / scanned page
const MEDIUM = payload(1024 * 1024 + 7); // 1 MiB PDF -> single PUT
const LARGE = payload(12 * 1024 * 1024); // 12 MiB -> resumable upload session (3 chunks)

const auth = { 'X-API-Key': KEY };
const noBody = { headers: auth, responseType: 'none' };

export function setup() {
  const seeded = { small: [], large: [] };
  for (let i = 0; i < 20; i++) {
    const r = http.post(`${BASE}/repositories/${REPO}/documents?folder=seed&fileName=img-${i}.png&conflict=replace`, SMALL,
      { headers: auth, responseType: 'text' });
    check(r, { 'seed small 201': (x) => x.status === 201 });
    seeded.small.push(r.json('id'));
  }
  for (let i = 0; i < 3; i++) {
    const r = http.post(`${BASE}/repositories/${REPO}/documents?folder=seed&fileName=big-${i}.pdf&conflict=replace`, LARGE,
      { headers: auth, responseType: 'text', timeout: '120s' });
    check(r, { 'seed large 201': (x) => x.status === 201 });
    seeded.large.push(r.json('id'));
  }
  return seeded;
}

let failuresLogged = 0;
function pick(a) { return a[Math.floor(Math.random() * a.length)]; }

export default function (seeded) {
  const roll = Math.random() * 100;
  const obj = `objects/BUS2081/${5100000000 + Math.floor(Math.random() * 500)}/documents`;
  let r, op;
  if (roll < 35) {
    op = 'download_small';
    r = http.get(`${BASE}/repositories/${REPO}/documents/${pick(seeded.small)}/content`, noBody);
    check(r, { 'download 200': (x) => x.status === 200 });
  } else if (roll < 40) {
    op = 'download_large';
    r = http.get(`${BASE}/repositories/${REPO}/documents/${pick(seeded.large)}/content`, Object.assign({ timeout: '120s' }, noBody));
    check(r, { 'download large 200': (x) => x.status === 200 });
  } else if (roll < 60) {
    op = 'metadata';
    r = http.get(`${BASE}/repositories/${REPO}/documents/${pick(seeded.small)}`, noBody);
    check(r, { 'metadata 200': (x) => x.status === 200 });
  } else if (roll < 70) {
    op = 'list';
    r = http.get(`${BASE}/repositories/${REPO}/${obj}?top=50`, noBody);
    check(r, { 'list 200': (x) => x.status === 200 });
  } else if (roll < 90) {
    op = 'upload_small';
    r = http.post(`${BASE}/repositories/${REPO}/${obj}?fileName=photo-${__VU}-${__ITER}.jpg`, SMALL, noBody);
    check(r, { 'upload small 201': (x) => x.status === 201 });
  } else if (roll < 97) {
    op = 'upload_medium';
    r = http.post(`${BASE}/repositories/${REPO}/${obj}?fileName=doc-${__VU}-${__ITER}.pdf`, MEDIUM, noBody);
    check(r, { 'upload medium 201': (x) => x.status === 201 });
  } else {
    op = 'upload_large';
    r = http.post(`${BASE}/repositories/${REPO}/${obj}?fileName=drawing-${__VU}-${__ITER}.pdf`, LARGE, Object.assign({ timeout: '180s' }, noBody));
    check(r, { 'upload large 201': (x) => x.status === 201 });
  }
  trends[op].add(r.timings.duration);
  if ((r.status === 0 || r.status >= 400) && failuresLogged < 3) {
    failuresLogged++;
    console.warn(`FAIL op=${op} status=${r.status} error_code=${r.error_code} error=${r.error}`);
  }
  sleep(0.1 + Math.random() * 0.4); // think time
}

export function handleSummary(data) {
  const m = data.metrics;
  const v = (name, stat) => (m[name] && m[name].values[stat] !== undefined ? m[name].values[stat] : NaN);
  const ms = (x) => (isNaN(x) ? '-' : x.toFixed(0));
  const secs = parseFloat(DURATION);
  const row = {
    vus: VUS,
    duration: DURATION,
    requests: v('http_reqs', 'count'),
    rps: v('http_reqs', 'rate'),
    errorRate: v('http_req_failed', 'rate'),
    checks: v('checks', 'rate'),
    median: Object.fromEntries(OPS.map((k) => [k, v(`op_${k}`, 'med')])),
    p95: Object.fromEntries(OPS.map((k) => [k, v(`op_${k}`, 'p(95)')])),
    p99: Object.fromEntries(OPS.map((k) => [k, v(`op_${k}`, 'p(99)')])),
    sentMBps: v('data_sent', 'count') / 1048576 / secs,
    receivedMBps: v('data_received', 'count') / 1048576 / secs,
  };
  const md = `| ${VUS} | ${row.requests} | ${row.rps.toFixed(1)} | ${(row.errorRate * 100).toFixed(2)}% | ` +
    OPS.map((k) => `${ms(row.median[k])} / ${ms(row.p95[k])}`).join(' | ') +
    ` | ${row.sentMBps.toFixed(1)} / ${row.receivedMBps.toFixed(1)} |\n`;
  return {
    [`results/summary-${VUS}.json`]: JSON.stringify(row, null, 2),
    [`results/row-${VUS}.md`]: md,
    stdout: `\nVUs=${VUS} reqs=${row.requests} rps=${row.rps.toFixed(1)} errors=${(row.errorRate * 100).toFixed(2)}% checks=${(row.checks * 100).toFixed(2)}%\n` +
      OPS.map((k) => `  ${k.padEnd(15)} med ${ms(row.median[k]).padStart(6)} ms   p95 ${ms(row.p95[k]).padStart(6)} ms   p99 ${ms(row.p99[k]).padStart(6)} ms`).join('\n') + '\n',
  };
}
