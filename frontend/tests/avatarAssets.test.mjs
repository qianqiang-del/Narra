import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const avatars = ['teacher', 'assist', 'clown', 'curious', 'note-taker', 'thinker']

function readPngHeader(file) {
  const data = fs.readFileSync(file)
  assert.deepEqual([...data.subarray(0, 8)], [137, 80, 78, 71, 13, 10, 26, 10])
  assert.equal(data.readUInt32BE(8), 13)
  assert.equal(data.toString('ascii', 12, 16), 'IHDR')
  return {
    width: data.readUInt32BE(16),
    height: data.readUInt32BE(20),
    colorType: data[25],
  }
}

test('discussion avatars keep the stable paths and transparent RGBA format', () => {
  for (const name of avatars) {
    const file = path.join(root, 'public', 'avatars', `${name}-2.png`)
    const header = readPngHeader(file)
    assert.equal(header.width, 256, name)
    assert.equal(header.height, 256, name)
    assert.equal(header.colorType, 6, `${name} must contain an alpha channel`)
  }
})
