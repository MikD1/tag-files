import {defineConfig,devices} from '@playwright/test';
export default defineConfig({
 testDir:'./e2e',testMatch:'**/*.spec.ts',testIgnore:['**/release/**','**/large/**','**/performance/**'],fullyParallel:false,workers:1,
 timeout:30_000,expect:{timeout:5_000},retries:0,
 reporter:[['list']],outputDir:'test-results',
 use:{baseURL:'http://127.0.0.1:15173',trace:'retain-on-failure',viewport:{width:1440,height:1000}},
 projects:[{name:'chromium',use:{...devices['Desktop Chrome']}},{name:'webkit',use:{...devices['Desktop Safari']}}],
 webServer:[
  {command:'go run .',cwd:'e2e/backend',url:'http://127.0.0.1:18080/api/v1/tags',timeout:60_000,reuseExistingServer:false},
  {command:'npm run build && npm exec vite preview -- --host 127.0.0.1 --port 15173 --strictPort',url:'http://127.0.0.1:15173',env:{TAG_FILES_API_ORIGIN:'http://127.0.0.1:18080'},timeout:30_000,reuseExistingServer:false},
 ],
});
