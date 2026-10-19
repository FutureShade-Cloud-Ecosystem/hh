// The first-dealer denylist, held against this app's own sources: no dealer
// city, brand, product, or planted demo word may live in the committed text,
// because the image exit test scans the built bundles for the same words and
// a hit here would fail the gate at deploy time.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const srcDir = fileURLToPath(new URL('../src', import.meta.url));

const DENYLIST = /Dibbits|Quinte|Trenton|Belleville|Kingston|Brampton|Techo|Unilock|Permacon|Holland paver/;

const walk = (dir) => {
	const out = [];
	for (const ent of readdirSync(dir, { withFileTypes: true })) {
		if (ent.isDirectory()) out.push(...walk(join(dir, ent.name)));
		else out.push(join(dir, ent.name));
	}
	return out;
};

test('no first-dealer denylist words in the manager-home sources', () => {
	const offences = [];
	for (const abs of walk(srcDir)) {
		const text = readFileSync(abs, 'utf8');
		const m = DENYLIST.exec(text);
		if (m !== null) offences.push(`${abs}: ${m[0]}`);
	}
	assert.deepEqual(offences, [], 'a denylist word lives in src; use the second dealer\'s words instead');
});
