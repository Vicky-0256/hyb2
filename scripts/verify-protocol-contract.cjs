#!/usr/bin/env node
/* Lightweight contract check; it does not require a JSON-Schema package. */
const fs = require('fs');
const path = require('path');

const root = path.resolve(__dirname, '..');
const specDir = path.join(root, 'desktop', 'spec');
const files = ['project.schema.json', 'task.schema.json', 'result.schema.json', 'capabilities.schema.json'];
const schemas = files.map((file) => {
  const value = JSON.parse(fs.readFileSync(path.join(specDir, file), 'utf8'));
  if (!value.$id || !value.$id.includes('hyb2.org/schemas/')) throw new Error(`${file}: missing stable $id`);
  return [file, value];
});

for (const [file, schema] of schemas) {
  const required = schema.required || [];
  if (file !== 'project.schema.json' && !required.includes('protocol_version')) {
    throw new Error(`${file}: protocol_version must be required`);
  }
  if (schema.properties && schema.properties.protocol_version && schema.properties.protocol_version.const !== '1.0') {
    throw new Error(`${file}: unsupported protocol version`);
  }
}

const task = schemas.find(([file]) => file === 'task.schema.json')[1];
const result = schemas.find(([file]) => file === 'result.schema.json')[1];
const capability = schemas.find(([file]) => file === 'capabilities.schema.json')[1];
for (const [name, schema, field] of [['task', task, 'task_id'], ['result', result, 'result_id'], ['capability', capability, 'capabilities']]) {
  if (!schema.properties[field]) throw new Error(`${name}: missing ${field}`);
}
if (result.$defs.provenance.properties.rules.properties.bin_size.const !== 10 ||
    result.$defs.provenance.properties.rules.properties.colour_cap.const !== '95') {
  throw new Error('result provenance no longer pins legacy contact-map semantics');
}
const fixtures = [['task.schema.json', 'task.json'], ['result.schema.json', 'result.json'], ['capabilities.schema.json', 'capabilities.json']];
for (const [schemaFile, fixtureFile] of fixtures) {
  const schema = JSON.parse(fs.readFileSync(path.join(specDir, schemaFile), 'utf8'));
  const fixture = JSON.parse(fs.readFileSync(path.join(specDir, 'fixtures', fixtureFile), 'utf8'));
  for (const field of schema.required || []) {
    if (!(field in fixture)) throw new Error(`${fixtureFile}: missing required field ${field}`);
  }
  if (fixture.protocol_version !== schema.properties.protocol_version.const) {
    throw new Error(`${fixtureFile}: protocol version mismatch`);
  }
}
console.log(`HYB2 protocol contract OK (${schemas.length} schemas, version 1.0)`);
