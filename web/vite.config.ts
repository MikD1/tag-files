import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';

export default defineConfig({
 plugins: [react()],
 server: { port: 5173, strictPort:true, proxy:{'/api':process.env.TAG_FILES_API_ORIGIN ?? 'http://127.0.0.1:8080','/media':process.env.TAG_FILES_API_ORIGIN ?? 'http://127.0.0.1:8080'} },
 build:{target:['chrome120','safari17'],sourcemap:false},
 test:{environment:'jsdom',setupFiles:['./src/test/setup.ts'],exclude:['e2e/**','node_modules/**']},
});
