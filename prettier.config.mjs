/** @type {import("prettier").Config} */
const config = {
    printWidth: 100,
    tabWidth: 4,
    trailingComma: "all",
    singleQuote: false,
    semi: false,
    bracketSpacing: true,
    plugins: ["prettier-plugin-astro"],
    overrides: [
        {
            files: "*.astro",
            options: {
                parser: "astro",
            },
        },
    ],
}

export default config
