"""Preserve deployed assets and publish a cache-isolated ES module graph.

The old entry name is a re-export, never a second application bundle.
No pricing logic is rewritten here. Run audit-* and pricing verification after.
"""
import argparse
import hashlib
import json
import pathlib
import re
import tarfile
import tempfile

NAMESPACE = "frontend-fix-20260925"
LEGACY_MAIN = "index-D4VkjDT5.js"


def sha(data):
    return hashlib.sha256(data).hexdigest()


def build(input_archive, output_archive, manifest_path):
    if input_archive.resolve() == output_archive.resolve():
        raise ValueError("Input and output archives must differ")
    with tempfile.TemporaryDirectory(prefix="newapi-module-fix-") as temp:
        root = pathlib.Path(temp)
        with tarfile.open(input_archive, "r:gz") as archive:
            archive.extractall(root, filter="data")
        before = {p.relative_to(root).as_posix(): sha(p.read_bytes())
                  for p in root.rglob("*") if p.is_file()}
        dist = root / "web/classic/dist"
        assets = dist / "assets"
        index = dist / "index.html"
        html = index.read_text(encoding="utf-8")
        match = re.search(r'<script type="module" crossorigin src="/assets/([^"/]+\.js)">', html)
        if not match:
            raise ValueError("Expected deployed root entry script not found")
        main = match.group(1)
        if main == LEGACY_MAIN or not (assets / main).is_file():
            raise ValueError("Expected renamed Luna entry")
        main_text = (assets / main).read_text(encoding="utf-8")
        if "Number.isFinite(e.group_ratio_override)&&(u=e.group_ratio_override)" not in main_text:
            raise ValueError("Luna price override missing")
        namespace = assets / NAMESPACE
        if namespace.exists():
            raise ValueError("Already namespaced; refusing recursive duplication")
        originals = [p for p in assets.rglob("*") if p.is_file()]
        namespace.mkdir()
        prefix = "assets/" + NAMESPACE + "/"
        # Vite preloads use root-relative assets/... strings, whereas static
        # imports use ./chunk.js. Leave relative imports untouched.
        path_pattern = re.compile(r'(["\'`])(/?)assets/')
        for source in originals:
            target = namespace / source.relative_to(assets)
            target.parent.mkdir(parents=True, exist_ok=True)
            data = source.read_bytes()
            if source.suffix in (".js", ".css"):
                text = data.decode("utf-8")
                text = path_pattern.sub(lambda m: m[1] + m[2] + prefix, text)
                text = text.replace("url(/assets/", "url(/" + prefix)
                data = text.encode("utf-8")
            target.write_bytes(data)
        forwarder = ('// Compatibility entry: one shared application module.\n'
                     f'export * from "./{main}";\n').encode()
        (namespace / LEGACY_MAIN).write_bytes(forwarder)
        # Also help already loaded pages with the root entry; fresh page loads
        # use the namespace and never rely on an old cached entry or old 404.
        (assets / LEGACY_MAIN).write_bytes(forwarder)
        html = html.replace('"/assets/', '"/' + prefix)
        index.write_text(html, encoding="utf-8", newline="")
        files = {p.relative_to(root).as_posix(): sha(p.read_bytes())
                 for p in root.rglob("*") if p.is_file()}
        allowed = {"web/classic/dist/index.html", "web/classic/dist/assets/" + LEGACY_MAIN}
        for name, digest in before.items():
            if name not in allowed and files.get(name) != digest:
                raise AssertionError("Unexpected modification: " + name)
        assert (namespace / main).exists()
        assert (namespace / LEGACY_MAIN).read_bytes() == forwarder
        output_archive.parent.mkdir(parents=True, exist_ok=True)
        with tarfile.open(output_archive, "w:gz") as archive:
            archive.add(root / "web", arcname="web")
        result = {
            "input_sha256": sha(input_archive.read_bytes()),
            "output_sha256": sha(output_archive.read_bytes()),
            "namespace": NAMESPACE, "main": main, "legacy_main": LEGACY_MAIN,
            "original_files": len(before), "total_files": len(files),
            "original_assets_unchanged": True, "files": files,
        }
        manifest_path.write_text(json.dumps(result, indent=2), encoding="utf-8")
        print(json.dumps({k: v for k, v in result.items() if k != "files"}))


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("input", type=pathlib.Path)
    parser.add_argument("output", type=pathlib.Path)
    parser.add_argument("manifest", type=pathlib.Path)
    args = parser.parse_args()
    build(args.input, args.output, args.manifest)
