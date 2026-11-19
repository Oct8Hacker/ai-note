#!/bin/sh
set -eu
cd "$(dirname "$0")/../extension"
node --input-type=module -e '
import {readFileSync} from "node:fs";
import {createHash} from "node:crypto";
const {key} = JSON.parse(readFileSync("manifest.json", "utf8"));
const digest = createHash("sha256").update(Buffer.from(key, "base64")).digest("hex");
console.log([...digest.slice(0,32)].map(n => String.fromCharCode(97 + parseInt(n,16))).join(""));
'
