import { defineConfig, envField } from "astro/config"
import tailwindcss from "@tailwindcss/vite"
import react from "@astrojs/react"
import node from "@astrojs/node"

// NOTE: for some reason with vite@7 pnpm dev does not work if ssr.noExternal is set to true or defined at all
const ssr = {}
if (process.argv.length > 2 && process.argv[2] == "build") {
    ssr.noExternal = true
}

export default defineConfig({
    adapter: node({ mode: "standalone" }),
    vite: {
        plugins: [tailwindcss()],
        ssr,
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
