import { defineConfig, envField } from "astro/config"
import tailwindcss from "@tailwindcss/vite"
import react from "@astrojs/react"
import node from "@astrojs/node"

// https://astro.build/config
export default defineConfig({
    adapter: node({ mode: "standalone" }),
    vite: {
        plugins: [tailwindcss()],
        ssr: {
            noExternal: true,
        },
    },
    integrations: [react()],
    env: {
        schema: {
            DB_USER: envField.string({ context: "server", access: "secret" }),
            DB_PASSWORD: envField.string({ context: "server", access: "secret" }),
            DB: envField.string({ context: "server", access: "secret" }),
            DB_HOST: envField.string({ context: "server", access: "secret" }),
            DB_PORT: envField.number({ context: "server", access: "secret" }),
            DOMAIN: envField.string({ context: "client", access: "public", default: "" }),
            PROD: envField.boolean({ context: "client", access: "public", default: false }),
        },
    },
})
