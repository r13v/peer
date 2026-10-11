import assert from "node:assert/strict"
import { test } from "node:test"
import { base64url } from "./base64url.ts"

test("a key is URL-safe base64 of the UTF-8 path without padding", () => {
	const path = "/home/ü/notes?.board"

	const key = base64url(path)

	assert.match(key, /^[A-Za-z0-9_-]+$/)
	assert.equal(Buffer.from(key, "base64url").toString("utf8"), path)
})
