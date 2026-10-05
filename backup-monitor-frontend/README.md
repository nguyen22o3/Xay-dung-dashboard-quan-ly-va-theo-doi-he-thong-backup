# React + TypeScript + Vite

## Cron Import / Export

The Cron task list exports and imports dashboard JSON files (maximum 64 KB):

```json
{
  "format": "backup-monitor-cron",
  "version": 1,
  "exportedAt": "2026-10-05T00:00:00Z",
  "jobs": [{ "id": "backup-site", "schedule": "30 4 * * *" }]
}
```

Only the six dashboard-managed tasks and the seven supported recurrence types
are accepted. aaPanel exports and arbitrary shell commands are not supported.
Exports contain schedules only, not credentials, scripts, paths, logs or backups.
Import preserves existing enabled/paused states; missing tasks are created enabled.
Tasks not listed in the file are untouched. Import does not execute scripts.

After file validation, a dialog shows current and imported schedules. No server
write occurs before confirmation. Every changed task is preflighted before any
write, then applied through the existing guarded server transaction with a fresh
preview token and verified by reading back the schedule. Storage and sources
cannot change through Import. The batch is sequential, not globally atomic:
failures stop later tasks and report verified saved items and uncertain writes.

Run regression tests with `node --test tests/cron-transfer.test.mjs`.

This template provides a minimal setup to get React working in Vite with HMR and some ESLint rules.

Currently, two official plugins are available:

- [@vitejs/plugin-react](https://github.com/vitejs/vite-plugin-react/blob/main/packages/plugin-react) uses [Oxc](https://oxc.rs)
- [@vitejs/plugin-react-swc](https://github.com/vitejs/vite-plugin-react/blob/main/packages/plugin-react-swc) uses [SWC](https://swc.rs/)

## React Compiler

The React Compiler is not enabled on this template because of its impact on dev & build performances. To add it, see [this documentation](https://react.dev/learn/react-compiler/installation).

## Expanding the ESLint configuration

If you are developing a production application, we recommend updating the configuration to enable type-aware lint rules:

```js
export default defineConfig([
  globalIgnores(['dist']),
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      // Other configs...

      // Remove tseslint.configs.recommended and replace with this
      tseslint.configs.recommendedTypeChecked,
      // Alternatively, use this for stricter rules
      tseslint.configs.strictTypeChecked,
      // Optionally, add this for stylistic rules
      tseslint.configs.stylisticTypeChecked,

      // Other configs...
    ],
    languageOptions: {
      parserOptions: {
        project: ['./tsconfig.node.json', './tsconfig.app.json'],
        tsconfigRootDir: import.meta.dirname,
      },
      // other options...
    },
  },
])

```

You can also install [eslint-plugin-react-x](https://npmx.dev/package/eslint-plugin-react-x) and [eslint-plugin-react-dom](https://npmx.dev/package/eslint-plugin-react-dom) for React-specific lint rules:

```js
// eslint.config.js
import reactX from 'eslint-plugin-react-x'
import reactDom from 'eslint-plugin-react-dom'

export default defineConfig([
  globalIgnores(['dist']),
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      // Other configs...
      // Enable lint rules for React
      reactX.configs['recommended-typescript'],
      // Enable lint rules for React DOM
      reactDom.configs.recommended,
    ],
    languageOptions: {
      parserOptions: {
        project: ['./tsconfig.node.json', './tsconfig.app.json'],
        tsconfigRootDir: import.meta.dirname,
      },
      // other options...
    },
  },
])

```
