#!/bin/sh
# Runs a command with a throwaway PostgreSQL on localhost and TEST_POSTGRES_DSN
# pointing at it. Used by the Dockerfile.smithd `test` stage so CI needs no services.
set -eu

export PGDATA=/tmp/pgdata
mkdir -p "$PGDATA" /run/postgresql
chown postgres:postgres "$PGDATA" /run/postgresql

su postgres -c "initdb -D $PGDATA -U postgres --auth=trust" >/dev/null
su postgres -c "pg_ctl -D $PGDATA -o '-c listen_addresses=localhost' -w start" >/dev/null
trap 'su postgres -c "pg_ctl -D $PGDATA -m fast stop" >/dev/null' EXIT

export TEST_POSTGRES_DSN="postgres://postgres@localhost:5432/postgres?sslmode=disable"
"$@"
