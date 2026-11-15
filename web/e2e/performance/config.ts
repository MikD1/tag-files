import {defineConfig, devices} from '@playwright/test';
if (!process.env.TAG_FILES_BROWSER_PERFORMANCE_ORIGIN || !process.env.TAG_FILES_BROWSER_PERFORMANCE_OUTPUT) {
 throw new Error('Use scripts/check-browser-performance with disposable actual images');
}
export default defineConfig({
 testDir:'.',testMatch:'first-use.spec.ts',workers:1,fullyParallel:false,retries:0,
 timeout:180_000,expect:{timeout:10_000},reporter:[['list']],
 outputDir:process.env.TAG_FILES_BROWSER_PERFORMANCE_OUTPUT,
 use:{baseURL:process.env.TAG_FILES_BROWSER_PERFORMANCE_ORIGIN,trace:'retain-on-failure',video:'off',viewport:{width:1440,height:1000}},
 projects:[{name:'chromium',use:{...devices['Desktop Chrome']}},{name:'webkit',use:{...devices['Desktop Safari']}}],
});
