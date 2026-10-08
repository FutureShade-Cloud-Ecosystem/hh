// A proof photo, made ready on the phone: the type is checked against what the
// core takes, and a camera sized picture is shrunk to a long side the screen
// of a delivery record never needs, so it travels fast on cellular and never
// meets the core's limit. Plain browser APIs, no framework.
import { PHOTO_LIMIT_BYTES, needsShrink, photoTypeFor, type PhotoType } from "./drive-logic.js";

export type ReadyPhoto = { ok: true; bytes: Uint8Array; type: PhotoType } | { ok: false; error: string };

const NOT_A_PICTURE = "A proof photo must be a JPEG, PNG or WebP picture.";
const TOO_BIG = "That photo is too big to send. Take it again and try once more.";
const LONG_SIDES = [1600, 1200, 800];
const QUALITIES = [0.82, 0.7, 0.6];

async function shrunk(file: File): Promise<Uint8Array | null> {
  if (typeof createImageBitmap !== "function") return null;
  let bitmap: ImageBitmap;
  try {
    bitmap = await createImageBitmap(file);
  } catch {
    return null;
  }
  try {
    for (let i = 0; i < LONG_SIDES.length; i += 1) {
      const longSide = LONG_SIDES[i] ?? 800;
      const scale = Math.min(1, longSide / Math.max(bitmap.width, bitmap.height));
      const canvas = document.createElement("canvas");
      canvas.width = Math.max(1, Math.round(bitmap.width * scale));
      canvas.height = Math.max(1, Math.round(bitmap.height * scale));
      const context = canvas.getContext("2d");
      if (context === null) return null;
      context.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
      const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, "image/jpeg", QUALITIES[i] ?? 0.6));
      if (blob !== null && blob.size > 0 && blob.size <= PHOTO_LIMIT_BYTES) return new Uint8Array(await blob.arrayBuffer());
    }
    return null;
  } finally {
    bitmap.close();
  }
}

/** Check the file, and shrink it when it is a camera sized picture. */
export async function readyPhoto(file: File): Promise<ReadyPhoto> {
  const type = photoTypeFor(file.type);
  if (type === null) return { ok: false, error: NOT_A_PICTURE };
  if (needsShrink(file.size)) {
    const small = await shrunk(file);
    if (small !== null) return { ok: true, bytes: small, type: "image/jpeg" };
    if (file.size > PHOTO_LIMIT_BYTES) return { ok: false, error: TOO_BIG };
  }
  return { ok: true, bytes: new Uint8Array(await file.arrayBuffer()), type };
}
