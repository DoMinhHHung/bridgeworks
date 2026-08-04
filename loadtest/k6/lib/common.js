import { Counter } from 'k6/metrics';

const allowedProfiles = new Set([
  'smoke',
  'baseline',
  'burst',
  'saturation',
  'dependency-degradation',
]);

const status200 = new Counter('status_200_total');
const status204 = new Counter('status_204_total');
const status400 = new Counter('status_400_total');
const status401 = new Counter('status_401_total');
const status403 = new Counter('status_403_total');
const status404 = new Counter('status_404_total');
const status409 = new Counter('status_409_total');
const status429 = new Counter('status_429_total');
const status500 = new Counter('status_500_total');
const status502 = new Counter('status_502_total');
const status503 = new Counter('status_503_total');
const status504 = new Counter('status_504_total');
const statusOther = new Counter('status_other_total');

function numberEnv(name, fallback) {
  const raw = __ENV[name];
  if (raw === undefined || raw === '') {
    return fallback;
  }
  const parsed = Number(raw);
  if (!Number.isFinite(parsed) || parsed <= 0) {
    throw new Error(`${name} must be a positive number`);
  }
  return parsed;
}

function smokeOptions() {
  return {
    scenarios: {
      traffic: {
        executor: 'constant-vus',
        vus: numberEnv('LOAD_VUS', 2),
        duration: __ENV.LOAD_DURATION || '5s',
        gracefulStop: '2s',
      },
    },
    thresholds: {
      checks: ['rate>0.90'],
      http_req_failed: ['rate<0.10'],
      http_req_duration: ['p(99)<5000'],
    },
  };
}

function baselineOptions() {
  return {
    scenarios: {
      traffic: {
        executor: 'constant-arrival-rate',
        rate: numberEnv('LOAD_RATE', 20),
        timeUnit: '1s',
        duration: __ENV.LOAD_DURATION || '2m',
        preAllocatedVUs: numberEnv('LOAD_PREALLOCATED_VUS', 20),
        maxVUs: numberEnv('LOAD_MAX_VUS', 100),
        gracefulStop: '10s',
      },
    },
    thresholds: {
      checks: ['rate>0.99'],
      http_req_failed: ['rate<0.01'],
      http_req_duration: ['p(95)<1000', 'p(99)<2500'],
    },
  };
}

function burstOptions() {
  return {
    scenarios: {
      traffic: {
        executor: 'ramping-vus',
        startVUs: numberEnv('LOAD_START_VUS', 2),
        stages: [
          { duration: __ENV.LOAD_BURST_RAMP || '5s', target: numberEnv('LOAD_BURST_VUS', 30) },
          { duration: __ENV.LOAD_BURST_HOLD || '20s', target: numberEnv('LOAD_BURST_VUS', 30) },
          { duration: __ENV.LOAD_BURST_RECOVERY || '10s', target: 2 },
        ],
        gracefulRampDown: '5s',
      },
    },
    thresholds: {
      checks: ['rate>0.98'],
      http_req_failed: ['rate<0.03'],
      http_req_duration: ['p(95)<2000', 'p(99)<5000'],
    },
  };
}

function saturationOptions() {
  return {
    scenarios: {
      traffic: {
        executor: 'ramping-arrival-rate',
        startRate: numberEnv('LOAD_START_RATE', 10),
        timeUnit: '1s',
        preAllocatedVUs: numberEnv('LOAD_PREALLOCATED_VUS', 50),
        maxVUs: numberEnv('LOAD_MAX_VUS', 300),
        stages: [
          { duration: __ENV.LOAD_SATURATION_STAGE || '1m', target: numberEnv('LOAD_SATURATION_RATE_1', 25) },
          { duration: __ENV.LOAD_SATURATION_STAGE || '1m', target: numberEnv('LOAD_SATURATION_RATE_2', 50) },
          { duration: __ENV.LOAD_SATURATION_STAGE || '1m', target: numberEnv('LOAD_SATURATION_RATE_3', 100) },
          { duration: __ENV.LOAD_SATURATION_STAGE || '1m', target: numberEnv('LOAD_SATURATION_RATE_4', 200) },
        ],
        gracefulStop: '15s',
      },
    },
    thresholds: {
      checks: [{ threshold: 'rate>0.95', abortOnFail: true, delayAbortEval: '15s' }],
      http_req_failed: [{ threshold: 'rate<0.05', abortOnFail: true, delayAbortEval: '15s' }],
      http_req_duration: [{ threshold: 'p(99)<5000', abortOnFail: true, delayAbortEval: '15s' }],
    },
  };
}

function degradationOptions() {
  return {
    scenarios: {
      traffic: {
        executor: 'constant-vus',
        vus: numberEnv('LOAD_VUS', 12),
        duration: __ENV.LOAD_DURATION || '45s',
        gracefulStop: '5s',
      },
    },
    thresholds: {
      checks: ['rate>0.40'],
      http_req_failed: ['rate<0.70'],
      http_req_duration: ['p(99)<10000'],
    },
  };
}

export function buildOptions(profile) {
  if (!allowedProfiles.has(profile)) {
    throw new Error(`unsupported load profile: ${profile}`);
  }
  let options;
  switch (profile) {
    case 'smoke':
      options = smokeOptions();
      break;
    case 'baseline':
      options = baselineOptions();
      break;
    case 'burst':
      options = burstOptions();
      break;
    case 'saturation':
      options = saturationOptions();
      break;
    case 'dependency-degradation':
      options = degradationOptions();
      break;
    default:
      throw new Error(`unsupported load profile: ${profile}`);
  }
  options.noConnectionReuse = false;
  options.discardResponseBodies = true;
  options.userAgent = 'bridgeworks-loadtest';
  return options;
}

export function recordStatus(status) {
  switch (status) {
    case 200:
      status200.add(1);
      break;
    case 204:
      status204.add(1);
      break;
    case 400:
      status400.add(1);
      break;
    case 401:
      status401.add(1);
      break;
    case 403:
      status403.add(1);
      break;
    case 404:
      status404.add(1);
      break;
    case 409:
      status409.add(1);
      break;
    case 429:
      status429.add(1);
      break;
    case 500:
      status500.add(1);
      break;
    case 502:
      status502.add(1);
      break;
    case 503:
      status503.add(1);
      break;
    case 504:
      status504.add(1);
      break;
    default:
      statusOther.add(1);
  }
}

function metricValue(data, name, field, fallback = 0) {
  const metric = data.metrics[name];
  if (!metric || !metric.values || metric.values[field] === undefined) {
    return fallback;
  }
  return metric.values[field];
}

function statusDistribution(data) {
  const names = [200, 204, 400, 401, 403, 404, 409, 429, 500, 502, 503, 504];
  const result = {};
  for (const status of names) {
    const count = metricValue(data, `status_${status}_total`, 'count', 0);
    if (count > 0) {
      result[String(status)] = count;
    }
  }
  const other = metricValue(data, 'status_other_total', 'count', 0);
  if (other > 0) {
    result.other = other;
  }
  return result;
}

function thresholdSummary(data) {
  const result = {};
  for (const [name, metric] of Object.entries(data.metrics)) {
    if (!metric.thresholds) {
      continue;
    }
    for (const [expression, threshold] of Object.entries(metric.thresholds)) {
      result[`${name}:${expression}`] = threshold.ok === true;
    }
  }
  return result;
}

export function summaryArtifact(data) {
  const durationMs = data.state && data.state.testRunDurationMs
    ? data.state.testRunDurationMs
    : 0;
  return {
    schema_version: 1,
    profile: __ENV.LOAD_PROFILE || 'unknown',
    scenario: __ENV.LOAD_SCENARIO || 'unknown',
    duration_ms: durationMs,
    request_count: metricValue(data, 'http_reqs', 'count', 0),
    failed_rate: metricValue(data, 'http_req_failed', 'rate', 0),
    latency_ms: {
      p50: metricValue(data, 'http_req_duration', 'p(50)', 0),
      p95: metricValue(data, 'http_req_duration', 'p(95)', 0),
      p99: metricValue(data, 'http_req_duration', 'p(99)', 0),
      max: metricValue(data, 'http_req_duration', 'max', 0),
    },
    checks_rate: metricValue(data, 'checks', 'rate', 0),
    dropped_iterations: metricValue(data, 'dropped_iterations', 'count', 0),
    status_distribution: statusDistribution(data),
    thresholds: thresholdSummary(data),
  };
}

export function writeSummary(data) {
  return {
    '/results/k6-summary.json': JSON.stringify(summaryArtifact(data), null, 2),
  };
}
