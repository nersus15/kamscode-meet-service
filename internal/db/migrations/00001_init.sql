-- +goose Up
CREATE TABLE users (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email         text NOT NULL UNIQUE, -- selalu disimpan lowercase
    name          text NOT NULL,
    password_hash text NOT NULL,        -- PHC string argon2id
    created_at    timestamptz NOT NULL DEFAULT now()
);

-- Token sesi tidak disimpan mentah: hanya SHA-256-nya, agar bocornya DB tidak membocorkan sesi.
CREATE TABLE sessions (
    token_hash bytea PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);
CREATE INDEX sessions_user_id_idx ON sessions (user_id);

CREATE TABLE rooms (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug       text NOT NULL UNIQUE, -- dipakai sebagai room ID di SFU dan di tautan
    name       text NOT NULL,
    owner_id   uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX rooms_owner_id_idx ON rooms (owner_id);

-- +goose Down
DROP TABLE rooms;
DROP TABLE sessions;
DROP TABLE users;
