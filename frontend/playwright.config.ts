import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './e2e',
  timeout: 45000,
  fullyParallel: false,
  retries: 0,
  reporter: 'list',
  use: {
    baseURL: process.env.WALLET_E2E_URL || 'https://wallet.korzadi.com',
    browserName: 'chromium',
    headless: true,
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure'
  }
});
