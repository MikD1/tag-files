import {defineConfig, devices} from '@playwright/test';
export default defineConfig({
 testDir:'.',testMatch:'**/*.spec.ts',workers:1,fullyParallel:false,retries:0,
 timeout:30*60_000,expect:{timeout:10_000},reporter:[['list']],outputDir:`../../../.large-upload.browser-results/${process.env.TAG_FILES_BROWSER_ENGINE??'manual'}`,
 use:{baseURL:process.env.TAG_FILES_BROWSER_LARGE_ORIGIN,trace:'off',video:'off',viewport:{width:1440,height:1000}},
 projects:[{name:'chromium',use:{...devices['Desktop Chrome']}},{name:'webkit',use:{...devices['Desktop Safari']}}],
});
