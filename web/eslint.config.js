import js from '@eslint/js';
import ts from 'typescript-eslint';
import hooks from 'eslint-plugin-react-hooks';
import refresh from 'eslint-plugin-react-refresh';
import globals from 'globals';
export default ts.config(
 {ignores:['dist/**','node_modules/**','src/api/schema.d.ts']},
 js.configs.recommended, ...ts.configs.recommended,
 {files:['**/*.cjs','scripts/**/*.mjs'],languageOptions:{globals:globals.node}},
 {files:['**/*.{ts,tsx}'],languageOptions:{globals:{...globals.browser,...globals.node}},plugins:{'react-hooks':hooks,'react-refresh':refresh},rules:{...hooks.configs.recommended.rules,'react-refresh/only-export-components':['warn',{allowConstantExport:true}]}},
);
