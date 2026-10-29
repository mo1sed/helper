"""Build a COFF .syso holding an icon resource (RT_ICON + RT_GROUP_ICON) for Go."""
import struct, sys

ico = open(sys.argv[1], "rb").read()
_, _, n = struct.unpack_from("<HHH", ico, 0)
images, grp = [], struct.pack("<HHH", 0, 1, n)
for i in range(n):
    w, h, c, r, planes, bpp, size, off = struct.unpack_from("<BBBBHHII", ico, 6 + 16 * i)
    images.append(ico[off:off + size])
    grp += struct.pack("<BBBBHHIH", w, h, c, r, planes, bpp, size, i + 1)

def rdir(entries):  # entries: list of (id, offset, is_dir)
    b = struct.pack("<IIHHHH", 0, 0, 0, 0, 0, len(entries))
    for i, off, d in entries:
        b += struct.pack("<II", i, off | (0x80000000 if d else 0))
    return b

DIR = lambda k: 16 + 8 * k
# layout: root | type dirs | name dirs | lang dirs | data entries | data
root_sz = DIR(2)
t_icon_sz, t_grp_sz = DIR(n), DIR(1)
lang_sz = DIR(1)
leaves = n + 1
o_root = 0
o_ticon = root_sz
o_tgrp = o_ticon + t_icon_sz
o_lang = o_tgrp + t_grp_sz
o_dent = o_lang + leaves * lang_sz
o_data = o_dent + leaves * 16

blobs = images + [grp]
data, data_offs = b"", []
for bl in blobs:
    data_offs.append(o_data + len(data))
    data += bl + b"\0" * ((-len(bl)) % 8)

out = rdir([(3, o_ticon, True), (14, o_tgrp, True)])
out += rdir([(i + 1, o_lang + i * lang_sz, True) for i in range(n)])
out += rdir([(1, o_lang + n * lang_sz, True)])
for k in range(leaves):
    out += rdir([(0x409, o_dent + k * 16, False)])
relocs = []
for k, bl in enumerate(blobs):
    relocs.append(len(out))
    out += struct.pack("<IIII", data_offs[k], len(bl), 0, 0)
assert len(out) == o_data
out += data

hdr_sz, sec_sz = 20, 40
p_raw = hdr_sz + sec_sz
p_rel = p_raw + len(out)
p_sym = p_rel + 10 * len(relocs)
coff = struct.pack("<HHIIIHH", 0x8664, 1, 0, p_sym, 1, 0, 0)
coff += struct.pack("<8sIIIIIIHHI", b".rsrc", 0, 0, len(out), p_raw, p_rel, 0, len(relocs), 0, 0x40000040)
coff += out
for r in relocs:
    coff += struct.pack("<IIH", r, 0, 3)  # IMAGE_REL_AMD64_ADDR32NB
coff += struct.pack("<8sIhHBB", b".rsrc", 0, 1, 0, 3, 0)
coff += struct.pack("<I", 4)
open(sys.argv[2], "wb").write(coff)
print("ok", n, "icons,", len(coff), "bytes")
