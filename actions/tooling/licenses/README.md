# SDK notice supplements

Most notices are copied verbatim from the locked npm installations. These
version-specific supplements cover omitted license files; they are inputs to
`../build.mjs`, require no network during generation, and must be reviewed when
their package versions change. They do not change dependency versions.

- `nodable-entities-3.1.0.txt`: upstream LICENSE at the npm package's `gitHead`,
  [ac48e7ea591da372be023a481875c747535812b3](https://github.com/nodable/val-parsers/blob/ac48e7ea591da372be023a481875c747535812b3/LICENSE).
  The npm archive includes only the MIT designation in its README/package.json.
- `binary-0.3.0.txt`: full standard MIT permission text, with attribution to
  James Halliday from the locked package.json author. Its package.json and
  README designate MIT but omit the text. No copyright year is asserted.
- `buffers-0.1.1.txt`: full standard MIT permission text, with attribution to
  James Halliday from the locked package.json author. The archive omits a license
  designation/text; the upstream repository's preserved
  [README](https://github.com/wkibbler/node-buffers/blob/f1875a9e13f6e4274ba1720679a6fd7561381c20/README.md#license)
  designates MIT/X11. This is supplemental attribution and permission text,
  not a claim that a copyright statement was present in the archive. No year is asserted.
- `chainsaw-0.1.0.txt`: preserved distribution
  [copyright file](https://github.com/deepin-community/node-chainsaw/blob/12e6e99517477fe18d7b78e3ae0cad8c984dd610/debian/copyright),
  including the upstream James Halliday attribution and full Expat permission
  text. The locked package.json designates MIT/X11 but omits the text. The
  distribution explains the inferred ownership and includes its own attribution.

The standard MIT text for binary/buffers uses the permission text shipped by the
same author's locked `traverse@0.3.10` dependency. The supplements explicitly
retain author attribution without inventing years. They are separate from the
root Go notice generator and inventory.
