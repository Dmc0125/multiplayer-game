import postgres from "postgres"
import { DB, DB_HOST, DB_PASSWORD, DB_PORT, DB_USER } from "astro:env/server"

export const db = postgres({
    host: DB_HOST,
    user: DB_USER,
    database: DB,
    password: DB_PASSWORD,
    port: DB_PORT,
})
