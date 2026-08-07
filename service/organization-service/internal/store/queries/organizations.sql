-- name: GetOrganizationByClerkID :one
SELECT id, clerk_organization_id, name, slug, status,
       legal_name, website, country, company_type, verification_status, trust_status,
       created_at, updated_at
FROM organization.organizations
WHERE clerk_organization_id = $1;

-- name: InsertOrganization :one
INSERT INTO organization.organizations (
    id, clerk_organization_id, name, slug, status
) VALUES ($1, $2, $3, $4, $5)
RETURNING id, clerk_organization_id, name, slug, status,
          legal_name, website, country, company_type, verification_status, trust_status,
          created_at, updated_at;

-- name: UpdateOrganizationProjection :one
UPDATE organization.organizations
SET name = $2,
    slug = $3,
    status = $4
WHERE clerk_organization_id = $1
RETURNING id, clerk_organization_id, name, slug, status,
          legal_name, website, country, company_type, verification_status, trust_status,
          created_at, updated_at;

-- name: MarkOrganizationDeleted :one
UPDATE organization.organizations
SET name = NULL,
    slug = NULL,
    status = 'deleted'
WHERE clerk_organization_id = $1
RETURNING id, clerk_organization_id, name, slug, status,
          legal_name, website, country, company_type, verification_status, trust_status,
          created_at, updated_at;
