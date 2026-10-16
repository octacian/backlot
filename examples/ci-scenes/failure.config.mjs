import config from './playwright.config.mjs';
export default { ...config, testDir: './faults', workers: 1 };
