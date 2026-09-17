-- 0180 — DIB-510 fix pass: the demo delivery photographs were a RED pixel.
--
-- PURPOSE. 0179 seeded two real PNG bytes so the proof strip exercises dispatch's actual photo route
-- rather than a mock. The pixel it used happened to decode to saturated red, and a 1×1 red pixel
-- scaled to a 68px thumbnail is two red squares on the invoice detail — which on a screen where red
-- means overdue reads as a fault, not as a photograph.
--
-- This replaces those two rows' bytes with a neutral mid-grey pixel. Nothing else changes: same rows,
-- same captions, same route, same size CHECK. It is a separate migration because 0179 is committed and
-- a committed migration is never edited (repo.md rule 4).
--
-- Guarded to the two fixed 0179 ids, so it touches no real photograph anywhere.

UPDATE delivery_pod_photos
   SET image_bytes = decode(
         '89504e470d0a1a0a0000000d494844520000000100000001080200000090' ||
         '7753de0000000c4944415408d763a8a8a8000003fb0177f6ea6f0000000049454e44ae426082', 'hex'),
       byte_size   = octet_length(decode(
         '89504e470d0a1a0a0000000d494844520000000100000001080200000090' ||
         '7753de0000000c4944415408d763a8a8a8000003fb0177f6ea6f0000000049454e44ae426082', 'hex'))
 WHERE id IN ('51000000-0000-4000-9000-0000000000d1'::uuid,
              '51000000-0000-4000-9000-0000000000d2'::uuid);
