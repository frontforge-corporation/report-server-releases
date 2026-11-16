# The example gallery's source

Each folder here is one example report, as its author keeps it: the RFML document (`template.rfml.json`), its sample data (`samples/`), the JSON Schema of its data (`schemas/`), its tests (`tests/`) and any images (`assets/`). `gallery.json` lists them in the order the studio shows them. The organisations, people, places and contact details in them are made up.

The studio offers them under **Start from an example** and **New report → From a template**: picking one copies it into your catalog ([Starting from a template](https://docs.frontforge.com/designer/examples)). Installs read them from `../gallery/`, which is built from this folder: an `index.json` and a `.zip` of each folder.

## Using them

- **Read them** here, to see how a report is put together in RFML ([the RFML reference](https://docs.frontforge.com/spec/rfml)).
- **Import one** in the studio: zip its folder, or take its `.zip` from `../gallery/`, and use **Import a report**.
- **Offline,** set `REPORTD_EXAMPLES_GALLERY` to this folder, or to `../gallery/`, in a clone of this repository.
- **As a template source:** with a licence that has template sources, add this repository as a git source, with `path` set to `examples` and `ref` to the branch or tag of your release ([Template sources](https://docs.frontforge.com/operations/template-sources)).

## Licence

They are dedicated to the public domain under CC0 1.0 Universal (`LICENSE.md`): copy, change and use them for anything, commercial use included, with no credit needed. CC0 covers the examples only, not Frontforge Report Server.
