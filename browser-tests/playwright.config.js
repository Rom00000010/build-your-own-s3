const { defineConfig } = require('@playwright/test');

module.exports = defineConfig({
  testDir: '.',
  use: { browserName: 'chromium' },
  reporter: 'list',
});
