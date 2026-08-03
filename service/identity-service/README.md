# identity-service

Identity boundary của BridgeWorks.

## Owns

- Mapping giữa Clerk user và BridgeWorks user.
- Local account lifecycle: `active`, `disabled`, `deleted`.
- Public opaque identifier `id_user`.
- Đồng bộ primary verified email tối thiểu cần thiết.

## Does not own

- Password, session, magic link, MFA hoặc email verification flow.
- Organization membership.
- Talent profile.
- Authorization rules của bounded context khác.

Clerk là identity provider và source of truth cho authentication. Bảng
`app.app_users` chỉ là local projection phục vụ domain authorization.

## First vertical slice

1. Verify Clerk webhook signature.
2. Process `user.created`, `user.updated`, `user.deleted` idempotently.
3. Upsert `app.app_users`.
4. Expose authenticated `GET /api/v1/me`.
5. Route traffic through APISIX.
