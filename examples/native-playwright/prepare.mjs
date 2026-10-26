import { mkdir, rm } from 'node:fs/promises';

// Backlot holds the reports output group through collection of this execution.
await rm('reports', { recursive: true, force: true });
await mkdir('reports');
