import { defineConfig, envField } from "astro/config"
import tailwindcss from "@tailwindcss/vite"
import react from "@astrojs/react"

// https://astro.build/config
export default defineConfig({
    vite: {
        plugins: [tailwindcss()],
    },
    integrations: [react()],
    env: {
        schema: {
            DB_USER: envField.string({ context: "server", access: "secret" }),
            DB_PASSWORD: envField.string({ context: "server", access: "secret" }),
            DB: envField.string({ context: "server", access: "secret" }),
            DB_HOST: envField.string({ context: "server", access: "secret" }),
            DB_PORT: envField.number({ context: "server", access: "secret" }),
            AUTH_URL: envField.string({ context: "server", access: "public" }),
        },
    },
})
