import http from 'k6/http';
import { check, sleep } from 'k6';

import { buildOptions, recordStatus, writeSummary } from './lib/common.js';

const profile = __ENV.LOAD_PROFILE || 'smoke';
const scenario = __ENV.LOAD_SCENARIO || '';
const baseURL = __ENV.BASE_URL || 'http://apisix:9080';
const authToken = __ENV.AUTH_TOKEN || '';

const paths = {
  identity_me: '/api/v1/me',
  organization_current: '/api/v1/organizations/current',
  organization_membership: '/api/v1/organizations/current/membership',
  organization_dependency: '/api/v1/organizations/current',
};

if (!Object.prototype.hasOwnProperty.call(paths, scenario)) {
  throw new Error(`unsupported authenticated-read scenario: ${scenario}`);
}
if (authToken === '') {
  throw new Error('AUTH_TOKEN is required');
}

export const options = buildOptions(profile);

export default function () {
  const response = http.get(`${baseURL}${paths[scenario]}`, {
    headers: {
      Authorization: `Bearer ${authToken}`,
      Accept: 'application/json',
    },
    tags: {
      load_scenario: scenario,
      traffic_class: 'authenticated_read',
    },
    timeout: '12s',
  });

  recordStatus(response.status);
  const degradation = profile === 'dependency-degradation';
  check(response, {
    'authenticated response is expected': (r) => degradation
      ? [200, 502, 503, 504].includes(r.status)
      : r.status === 200,
  });
  sleep(Number(__ENV.LOAD_SLEEP_SECONDS || '0.05'));
}

export function handleSummary(data) {
  return writeSummary(data);
}
