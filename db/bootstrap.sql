-- One Postgres instance, one database, one schema and one role per service.
-- The boundary between services is enforced by database permissions rather
-- than by convention: catalog_svc physically cannot read identity's tables.
--
-- Run once:  psql -d postgres -f db/bootstrap.sql

SELECT 'CREATE DATABASE localyoutube'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'localyoutube') \gexec

\connect localyoutube

DO $$
DECLARE
  svc text;
BEGIN
  FOREACH svc IN ARRAY ARRAY['identity', 'catalog', 'ingest', 'recsys'] LOOP
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = svc || '_svc') THEN
      EXECUTE format('CREATE ROLE %I LOGIN PASSWORD %L', svc || '_svc', svc || '_dev');
    END IF;

    EXECUTE format('CREATE SCHEMA IF NOT EXISTS %I AUTHORIZATION %I', svc, svc || '_svc');

    -- Each role sees only its own schema. No cross-schema reads, ever.
    EXECUTE format('ALTER ROLE %I SET search_path = %I', svc || '_svc', svc);
    EXECUTE format('REVOKE ALL ON SCHEMA public FROM %I', svc || '_svc');
  END LOOP;
END
$$;
