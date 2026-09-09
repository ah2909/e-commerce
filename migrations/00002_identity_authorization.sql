-- +goose Up
CREATE TABLE users (
    store_id uuid NOT NULL REFERENCES stores (store_id),
    user_id uuid NOT NULL,
    email text,
    canonical_email text,
    status text NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT users_email_pair CHECK ((email IS NULL) = (canonical_email IS NULL)),
    CONSTRAINT users_email_valid CHECK (email IS NULL OR (email = btrim(email) AND char_length(email) BETWEEN 3 AND 254)),
    CONSTRAINT users_canonical_email_valid CHECK (canonical_email IS NULL OR canonical_email = lower(canonical_email)),
    CONSTRAINT users_status_valid CHECK (status IN ('active', 'disabled')),
    CONSTRAINT users_timestamps_valid CHECK (updated_at >= created_at),
    PRIMARY KEY (store_id, user_id)
);

CREATE UNIQUE INDEX users_store_email_uidx ON users (store_id, canonical_email) WHERE canonical_email IS NOT NULL;

CREATE TABLE identity_password_credentials (
    store_id uuid NOT NULL,
    user_id uuid NOT NULL,
    password_hash text NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (store_id, user_id),
    FOREIGN KEY (store_id, user_id) REFERENCES users (store_id, user_id) ON DELETE CASCADE,
    CONSTRAINT identity_password_hash_valid CHECK (char_length(password_hash) BETWEEN 32 AND 512)
);

CREATE TABLE identity_social_identities (
    store_id uuid NOT NULL,
    provider text NOT NULL,
    provider_subject text NOT NULL,
    user_id uuid NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (store_id, provider, provider_subject),
    UNIQUE (store_id, user_id, provider),
    FOREIGN KEY (store_id, user_id) REFERENCES users (store_id, user_id) ON DELETE CASCADE,
    CONSTRAINT identity_social_provider_valid CHECK (provider IN ('google', 'facebook')),
    CONSTRAINT identity_social_subject_valid CHECK (char_length(provider_subject) BETWEEN 1 AND 255)
);

CREATE TABLE identity_sessions (
    store_id uuid NOT NULL,
    session_id uuid NOT NULL,
    user_id uuid NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    compromised_at timestamptz,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (store_id, session_id),
    FOREIGN KEY (store_id, user_id) REFERENCES users (store_id, user_id) ON DELETE CASCADE,
    CONSTRAINT identity_sessions_expiry_valid CHECK (expires_at > created_at),
    CONSTRAINT identity_sessions_revocation_valid CHECK (revoked_at IS NULL OR revoked_at >= created_at),
    CONSTRAINT identity_sessions_compromise_valid CHECK (compromised_at IS NULL OR compromised_at >= created_at)
);

CREATE INDEX identity_sessions_user_idx ON identity_sessions (store_id, user_id, created_at DESC);
CREATE INDEX identity_sessions_expiry_idx ON identity_sessions (store_id, expires_at);

CREATE TABLE identity_access_tokens (
    store_id uuid NOT NULL,
    token_digest bytea NOT NULL,
    session_id uuid NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (store_id, token_digest),
    FOREIGN KEY (store_id, session_id) REFERENCES identity_sessions (store_id, session_id) ON DELETE CASCADE,
    CONSTRAINT identity_access_digest_valid CHECK (octet_length(token_digest) = 32),
    CONSTRAINT identity_access_expiry_valid CHECK (expires_at > created_at)
);

CREATE INDEX identity_access_session_idx ON identity_access_tokens (store_id, session_id);

CREATE TABLE identity_refresh_tokens (
    store_id uuid NOT NULL,
    token_digest bytea NOT NULL,
    session_id uuid NOT NULL,
    expires_at timestamptz NOT NULL,
    used_at timestamptz,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (store_id, token_digest),
    FOREIGN KEY (store_id, session_id) REFERENCES identity_sessions (store_id, session_id) ON DELETE CASCADE,
    CONSTRAINT identity_refresh_digest_valid CHECK (octet_length(token_digest) = 32),
    CONSTRAINT identity_refresh_expiry_valid CHECK (expires_at > created_at)
);

CREATE INDEX identity_refresh_session_idx ON identity_refresh_tokens (store_id, session_id);

CREATE TABLE identity_guests (
    store_id uuid NOT NULL,
    guest_id uuid NOT NULL,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (store_id, guest_id),
    CONSTRAINT identity_guests_expiry_valid CHECK (expires_at > created_at)
);

CREATE TABLE identity_guest_sessions (
    store_id uuid NOT NULL,
    session_id uuid NOT NULL,
    guest_id uuid NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    compromised_at timestamptz,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (store_id, session_id),
    FOREIGN KEY (store_id, guest_id) REFERENCES identity_guests (store_id, guest_id) ON DELETE CASCADE,
    CONSTRAINT identity_guest_sessions_expiry_valid CHECK (expires_at > created_at),
    CONSTRAINT identity_guest_sessions_revocation_valid CHECK (revoked_at IS NULL OR revoked_at >= created_at),
    CONSTRAINT identity_guest_sessions_compromise_valid CHECK (compromised_at IS NULL OR compromised_at >= created_at)
);

CREATE TABLE identity_guest_access_tokens (
    store_id uuid NOT NULL,
    token_digest bytea NOT NULL,
    session_id uuid NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (store_id, token_digest),
    FOREIGN KEY (store_id, session_id) REFERENCES identity_guest_sessions (store_id, session_id) ON DELETE CASCADE,
    CONSTRAINT identity_guest_access_digest_valid CHECK (octet_length(token_digest) = 32),
    CONSTRAINT identity_guest_access_expiry_valid CHECK (expires_at > created_at)
);

CREATE TABLE identity_guest_refresh_tokens (
    store_id uuid NOT NULL,
    token_digest bytea NOT NULL,
    session_id uuid NOT NULL,
    expires_at timestamptz NOT NULL,
    used_at timestamptz,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (store_id, token_digest),
    FOREIGN KEY (store_id, session_id) REFERENCES identity_guest_sessions (store_id, session_id) ON DELETE CASCADE,
    CONSTRAINT identity_guest_refresh_digest_valid CHECK (octet_length(token_digest) = 32),
    CONSTRAINT identity_guest_refresh_expiry_valid CHECK (expires_at > created_at)
);

INSERT INTO users (store_id, user_id, status, created_at, updated_at)
SELECT store_id, user_id, 'active', created_at, updated_at
FROM store_memberships
ON CONFLICT (store_id, user_id) DO NOTHING;

ALTER TABLE store_memberships ADD COLUMN role text;
UPDATE store_memberships SET role = 'owner' WHERE role IS NULL;
ALTER TABLE store_memberships ALTER COLUMN role SET NOT NULL;
ALTER TABLE store_memberships ADD CONSTRAINT store_memberships_role_valid CHECK (role IN ('owner', 'admin', 'staff'));
ALTER TABLE store_memberships ADD CONSTRAINT store_memberships_user_fk FOREIGN KEY (store_id, user_id) REFERENCES users (store_id, user_id);

CREATE TABLE audit_records (
    store_id uuid NOT NULL,
    audit_id uuid NOT NULL,
    actor_type text NOT NULL,
    actor_id text NOT NULL,
    action text NOT NULL,
    subject_type text NOT NULL,
    subject_id uuid NOT NULL,
    request_id text NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (store_id, audit_id),
    CONSTRAINT audit_actor_type_valid CHECK (actor_type IN ('platform_admin', 'user')),
    CONSTRAINT audit_actor_id_valid CHECK (char_length(actor_id) BETWEEN 1 AND 128),
    CONSTRAINT audit_action_valid CHECK (action IN ('store.created', 'membership.created', 'membership.updated')),
    CONSTRAINT audit_subject_valid CHECK (subject_type IN ('store', 'membership')),
    CONSTRAINT audit_request_id_valid CHECK (char_length(request_id) BETWEEN 1 AND 128),
    CONSTRAINT audit_metadata_object CHECK (jsonb_typeof(metadata) = 'object')
);

CREATE INDEX audit_records_store_occurred_idx ON audit_records (store_id, occurred_at DESC);
