import http from 'k6/http';
import { check, sleep } from 'k6';
import crypto from 'k6/crypto';
import encoding from 'k6/encoding';

import { buildOptions, recordStatus, writeSummary } from './lib/common.js';

const profile = __ENV.LOAD_PROFILE || 'smoke';
const scenario = __ENV.LOAD_SCENARIO || '';
const baseURL = __ENV.BASE_URL || 'http://apisix:9080';
const webhookSecret = __ENV.WEBHOOK_SECRET || '';
const runNonce = (__ENV.RUN_NONCE || 'load').replace(/[^A-Za-z0-9_-]/g, '');
const seededOrganizationID = __ENV.WEBHOOK_ORGANIZATION_ID || '';
const eventTimestampMs = Number(__ENV.WEBHOOK_EVENT_TIMESTAMP_MS || Date.now());

const allowedScenarios = new Set([
  'identity_user_unique',
  'identity_user_retry',
  'organization_unique',
  'organization_retry',
  'membership_unique',
  'membership_retry',
]);

if (!allowedScenarios.has(scenario)) {
  throw new Error(`unsupported webhook scenario: ${scenario}`);
}
if (!webhookSecret.startsWith('whsec_')) {
  throw new Error('WEBHOOK_SECRET must use the whsec_ format');
}
if (scenario.startsWith('membership_') && seededOrganizationID === '') {
  throw new Error('WEBHOOK_ORGANIZATION_ID is required for membership scenarios');
}

const signingKey = encoding.b64decode(webhookSecret.slice('whsec_'.length), 'std');

export const options = buildOptions(profile);

function sequence() {
  return `${runNonce}_${__VU}_${__ITER}`;
}

function eventID(unique) {
  return unique ? `msg_load_${sequence()}` : `msg_load_retry_${runNonce}_${scenario}`;
}

function identityPayload(unique) {
  const suffix = unique ? sequence() : `${runNonce}_retry`;
  return JSON.stringify({
    type: 'user.deleted',
    timestamp: eventTimestampMs,
    data: { id: `user_load_${suffix}` },
  });
}

function organizationPayload(unique) {
  const suffix = unique ? sequence() : `${runNonce}_retry`;
  return JSON.stringify({
    type: 'organization.created',
    timestamp: eventTimestampMs,
    data: {
      id: `org_load_${suffix}`,
      name: 'Load Fixture',
      slug: `load-${suffix}`.slice(0, 80),
    },
  });
}

function membershipPayload(unique) {
  const suffix = unique ? sequence() : `${runNonce}_retry`;
  return JSON.stringify({
    type: 'organizationMembership.deleted',
    timestamp: eventTimestampMs,
    data: {
      id: `mem_load_${suffix}`,
      organization: { id: seededOrganizationID },
      public_user_data: { user_id: `user_load_membership_${suffix}` },
      role: 'org:member',
    },
  });
}

function requestDefinition() {
  const unique = scenario.endsWith('_unique');
  if (scenario.startsWith('identity_user_')) {
    return {
      url: `${baseURL}/api/v1/identity/webhooks/clerk`,
      payload: identityPayload(unique),
      unique,
    };
  }
  if (scenario.startsWith('organization_')) {
    return {
      url: `${baseURL}/api/v1/organizations/webhooks/clerk`,
      payload: organizationPayload(unique),
      unique,
    };
  }
  return {
    url: `${baseURL}/api/v1/organizations/webhooks/clerk`,
    payload: membershipPayload(unique),
    unique,
  };
}

export default function () {
  const definition = requestDefinition();
  const id = eventID(definition.unique);
  const timestamp = Math.floor(Date.now() / 1000).toString();
  const signedContent = `${id}.${timestamp}.${definition.payload}`;
  const signature = crypto.hmac('sha256', signingKey, signedContent, 'base64');

  const response = http.post(definition.url, definition.payload, {
    headers: {
      'Content-Type': 'application/json',
      'svix-id': id,
      'svix-timestamp': timestamp,
      'svix-signature': `v1,${signature}`,
    },
    tags: {
      load_scenario: scenario,
      traffic_class: 'webhook',
    },
    timeout: '15s',
  });

  recordStatus(response.status);
  const degradation = profile === 'dependency-degradation';
  check(response, {
    'webhook response is expected': (r) => degradation
      ? [204, 503].includes(r.status)
      : r.status === 204,
  });
  sleep(Number(__ENV.LOAD_SLEEP_SECONDS || '0.01'));
}

export function handleSummary(data) {
  return writeSummary(data);
}
