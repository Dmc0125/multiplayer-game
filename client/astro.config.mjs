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
    site: "https://paddle.dmnk.app",
    integrations: [react()],
    env: {
        schema: {
            DB_USER: envField.string({ context: "server", access: "secret" }),
            DB_PASSWORD: envField.string({ context: "server", access: "secret" }),
            DB: envField.string({ context: "server", access: "secret" }),
            DB_HOST: envField.string({ context: "server", access: "secret" }),
            DB_PORT: envField.number({ context: "server", access: "secret" }),
        },
    },
})
