-- name: GetOrganizationByClerkID :one
SELECT id, clerk_organization_id, name, slug, status, created_at, updated_at,
       legal_name, website, country, company_type, verification_status, trust_status,
       clerk_created_by_user_id, owner_bootstrapped, owner_bootstrap_eligible
FROM organization.organizations
WHERE clerk_organization_id = $1;

-- name: LockOrganizationByID :one
SELECT id, clerk_organization_id, name, slug, status, created_at, updated_at,
       legal_name, website, country, company_type, verification_status, trust_status,
       clerk_created_by_user_id, owner_bootstrapped, owner_bootstrap_eligible
FROM organization.organizations
WHERE id = $1
FOR UPDATE;

-- name: InsertOrganization :one
INSERT INTO organization.organizations (
    id, clerk_organization_id, name, slug, status, clerk_created_by_user_id
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, clerk_organization_id, name, slug, status, created_at, updated_at,
          legal_name, website, country, company_type, verification_status, trust_status,
          clerk_created_by_user_id, owner_bootstrapped, owner_bootstrap_eligible;

-- name: UpdateOrganizationProjection :exec
UPDATE organization.organizations
SET name = $2,
    slug = $3,
    status = $4
WHERE clerk_organization_id = $1;

-- name: SetOrganizationCreator :one
UPDATE organization.organizations
SET clerk_created_by_user_id = $2
WHERE clerk_organization_id = $1
  AND clerk_created_by_user_id IS NULL
RETURNING id, clerk_organization_id, name, slug, status, created_at, updated_at,
          legal_name, website, country, company_type, verification_status, trust_status,
          clerk_created_by_user_id, owner_bootstrapped, owner_bootstrap_eligible;

-- name: DisableOrganizationOwnerBootstrapEligibility :exec
UPDATE organization.organizations
SET owner_bootstrap_eligible = false
WHERE id = $1
  AND owner_bootstrap_eligible = true
  AND owner_bootstrapped = false;

-- name: MarkOrganizationOwnerBootstrapped :exec
UPDATE organization.organizations
SET owner_bootstrapped = true
WHERE id = $1
  AND owner_bootstrap_eligible = true
  AND owner_bootstrapped = false
  AND status = 'active'
  AND clerk_created_by_user_id IS NOT NULL;

-- name: UpdateOrganizationProductProfile :one
UPDATE organization.organizations
SET legal_name = $2,
    website = $3,
    country = $4,
    company_type = $5
WHERE id = $1
RETURNING id, clerk_organization_id, name, slug, status, created_at, updated_at,
          legal_name, website, country, company_type, verification_status, trust_status,
          clerk_created_by_user_id, owner_bootstrapped, owner_bootstrap_eligible;

-- name: UpdateOrganizationVerificationStatus :one
UPDATE organization.organizations
SET verification_status = $2
WHERE id = $1
RETURNING id, clerk_organization_id, name, slug, status, created_at, updated_at,
          legal_name, website, country, company_type, verification_status, trust_status,
          clerk_created_by_user_id, owner_bootstrapped, owner_bootstrap_eligible;

-- name: MarkOrganizationDeleted :one
UPDATE organization.organizations
SET name = NULL,
    slug = NULL,
    status = 'deleted'
WHERE clerk_organization_id = $1
RETURNING id, clerk_organization_id, name, slug, status, created_at, updated_at,
          legal_name, website, country, company_type, verification_status, trust_status,
          clerk_created_by_user_id, owner_bootstrapped, owner_bootstrap_eligible;
