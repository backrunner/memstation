env "ent" {
  url = env("DATABASE_URL")
  migration {
    dir = "file://ent/migrate/migrations"
  }
}
