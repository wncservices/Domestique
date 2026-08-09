-- The Go suite drops and recreates its tables, so it must not share a database
-- with the running app: `just docker-test` would wipe the routes you uploaded
-- with `just up`. Same server, separate database.
CREATE DATABASE domestique_test OWNER domestique;
