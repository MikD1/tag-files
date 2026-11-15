import {defineConfig,devices} from '@playwright/test';
export default defineConfig({
 testDir:'./e2e/release',testMatch:'**/*.spec.ts',workers:1,fullyParallel:false,retries:0,
 timeout:90_000,expect:{timeout:10_000},reporter:[['list']],outputDir:'test-results/release',
 use:{baseURL:process.env.TAG_FILES_RELEASE_ORIGIN??'http://127.0.0.1:8080',trace:'retain-on-failure',viewport:{width:1440,height:1000}},
 projects:[{name:'chromium',use:{...devices['Desktop Chrome']}},{name:'webkit',use:{...devices['Desktop Safari']}}],
});
