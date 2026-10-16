import { mkdir, copyFile } from 'node:fs/promises';

// Backlot holds the checkout output group through execution and collection.
await mkdir('build', { recursive: true });
await copyFile('app.mjs', 'build/app.mjs');
console.log('application built');
