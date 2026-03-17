#!/usr/bin/env python3
"""Generate a neumorphic dialog box PNG — 2x retina (2056×1329 @ 2x scale).

Logical size: ~600×360 px  →  drawn at 1200×720 px (SCALE=2).

Shadow model (derived from pixel analysis + math):
  DARK_SH vs SURFACE creates 162 delta units per alpha.
  LIGHT_SH vs SURFACE creates only 59 delta units per alpha (2.75:1 ratio).
  With equal offsets, the dark shadow's larger sigma always overwhelms the bright.

  Fix: ASYMMETRIC OFFSETS.
    Light shadow: small offset (close to element) + moderate blur  → bright edge right at element
    Dark  shadow: large offset (further away)     + larger  blur  → soft depth shadow further out
  This carves out a zone near the element where light dominates.
"""

from PIL import Image, ImageDraw, ImageFilter, ImageFont

# ── Palette ────────────────────────────────────────────────────────────────────
SURFACE  = (232, 230, 244)   # slightly purple-tinted white
DARK_SH  = (176, 173, 195)   # ~15% darker
LIGHT_SH = (255, 255, 255)   # white highlight
TEXT_COL = (78,  72, 112)    # dark purple-gray
ICON_COL = (105, 99, 148)    # medium purple-gray

# ── Scale ──────────────────────────────────────────────────────────────────────
SCALE = 2   # 2x retina; all layout values are logical px, call px() to convert.

def px(n):
    return round(n * SCALE)

# Canvas: 600×360 logical  (→ 1200×720 drawn)
CW, CH = px(600), px(360)

# ── Shadow helpers ─────────────────────────────────────────────────────────────

def _shadow_layer(size, x1, y1, x2, y2, r, color, alpha, blur):
    sh = Image.new('RGBA', size, (0, 0, 0, 0))
    ImageDraw.Draw(sh).rounded_rectangle(
        [x1, y1, x2, y2], radius=r, fill=color + (alpha,))
    return sh.filter(ImageFilter.GaussianBlur(blur))


def neu_raised(img, x1, y1, x2, y2, r,
               light_off=2, light_blur=4,
               dark_off=9,  dark_blur=9,
               dark_alpha=90, light_alpha=250):
    """Raised neumorphic rectangle — asymmetric offsets + tuned alphas.

    With dark_off=N and sigma=S, the wrong-side bleed fraction is Φ(-N·√2/S).
    dark_alpha must be low enough that  dark_alpha × bleed_fraction × 162/255  is
    imperceptible (< ~6 delta), while still giving a visible shadow on the correct side.

    Rule of thumb: dark_alpha ≈ 90  (bleed ≈ 6 delta, main shadow ≈ 28 delta)
                   light_alpha ≈ 250 (always safe — light never "bleeds wrong")
    """
    img.alpha_composite(
        _shadow_layer(img.size, x1+dark_off, y1+dark_off, x2+dark_off, y2+dark_off,
                      r, DARK_SH, dark_alpha, dark_blur))
    img.alpha_composite(
        _shadow_layer(img.size, x1-light_off, y1-light_off, x2-light_off, y2-light_off,
                      r, LIGHT_SH, light_alpha, light_blur))
    ImageDraw.Draw(img).rounded_rectangle(
        [x1, y1, x2, y2], radius=r, fill=SURFACE + (255,))


def neu_inset(img, x1, y1, x2, y2, r, off=2, dark_blur=6, light_blur=3):
    """Inset neumorphic rectangle."""
    ImageDraw.Draw(img).rounded_rectangle(
        [x1, y1, x2, y2], radius=r, fill=SURFACE + (255,))
    clip = Image.new('L', img.size, 0)
    ImageDraw.Draw(clip).rounded_rectangle([x1, y1, x2, y2], radius=r, fill=255)
    for color, ox, oy, a, blur in [
        (DARK_SH,  -off, -off, 190, dark_blur),
        (LIGHT_SH, +off, +off, 190, light_blur),
    ]:
        sh = _shadow_layer(img.size, x1+ox, y1+oy, x2+ox, y2+oy, r, color, a, blur)
        clipped = Image.new('RGBA', img.size, (0, 0, 0, 0))
        clipped.paste(sh, mask=clip)
        img.alpha_composite(clipped)


def groove(img, x1, y, x2):
    neu_inset(img, x1, y, x2, y + px(5), r=px(3), off=1, dark_blur=3, light_blur=2)


# ── Icons ──────────────────────────────────────────────────────────────────────

def icon_x(draw, cx, cy, sz, lw):
    draw.line([cx-sz, cy-sz, cx+sz, cy+sz], fill=ICON_COL + (235,), width=lw)
    draw.line([cx+sz, cy-sz, cx-sz, cy+sz], fill=ICON_COL + (235,), width=lw)


def icon_check(draw, cx, cy, sz, lw):
    pts = [
        (cx - sz,        cy + 1),
        (cx - sz//3 + 1, cy + sz*2//3 + 1),
        (cx + sz,        cy - sz*2//3),
    ]
    draw.line(pts, fill=ICON_COL + (235,), width=lw)


# ── Font ───────────────────────────────────────────────────────────────────────

def load_font(size, index=0):
    for path in [
        '/System/Library/Fonts/Helvetica.ttc',
        '/Library/Fonts/Arial.ttf',
        '/System/Library/Fonts/Supplemental/Arial.ttf',
    ]:
        try:
            return ImageFont.truetype(path, size, index=index)
        except Exception:
            pass
    return ImageFont.load_default()


# ── Layout (all in logical pixels) ────────────────────────────────────────────
#
#  Canvas 600×360 logical
#  ┌────────────────────────────────────────────────────────────────┐
#  │  Dialog 450×235 @ (75, 60)                                     │
#  │  ┌──────────────────────────────────────────────────────────┐  │
#  │  │  "Confirm Delete"  (title, y=18 from top)                │  │
#  │  │  ┌────────────────────────────────────────────────────┐  │  │
#  │  │  │  "Are you sure …"  (inset, y=34, h=75)             │  │  │
#  │  │  └────────────────────────────────────────────────────┘  │  │
#  │  │  ─── groove ───────────────────────────────────────────  │  │
#  │  │   [✕ cancel]                          [✓ confirm]         │  │
#  │  └──────────────────────────────────────────────────────────┘  │
#  └────────────────────────────────────────────────────────────────┘

def main():
    img = Image.new('RGBA', (CW, CH), SURFACE + (255,))

    # Dialog box (raised, HEAVY) ── logical 450×235 @ (75, 60)
    DX, DY, DW, DH = px(75), px(60), px(450), px(235)
    neu_raised(img, DX, DY, DX+DW, DY+DH,
               r=px(18), light_off=6, light_blur=10, dark_off=22, dark_blur=22,
               dark_alpha=140, light_alpha=255)

    # Title — 21pt bold
    font_title = load_font(px(21), index=1)
    ImageDraw.Draw(img).text(
        (DX + DW//2, DY + px(18)), "Confirm Delete",
        fill=TEXT_COL + (200,), font=font_title, anchor="mm")

    # Text area (inset) ── logical h=75, 22px margin each side
    TX, TY = DX + px(22), DY + px(34)
    TW, TH = DW - px(44), px(75)
    neu_inset(img, TX, TY, TX+TW, TY+TH,
              r=px(10), off=2, dark_blur=6, light_blur=3)

    font_body = load_font(px(14))
    ImageDraw.Draw(img).text(
        (TX + TW//2, TY + TH//2),
        "Are you sure you want to delete foo.txt?",
        fill=TEXT_COL + (225,), font=font_body, anchor="mm")

    # Groove ── 16px below text, 27px margins
    GY  = TY + TH + px(16)
    groove(img, DX + px(27), GY, DX + DW - px(27))

    # Buttons ── logical 108×44, 65px from dialog edges
    BW, BH = px(108), px(44)
    BY = GY + px(5) + px(16)

    # Cancel (inset, HEAVY, left)
    BCX = DX + px(65)
    neu_inset(img, BCX, BY, BCX+BW, BY+BH,
              r=px(10), off=4, dark_blur=10, light_blur=6)
    icon_x(ImageDraw.Draw(img), BCX + BW//2, BY + BH//2,
           sz=px(8), lw=px(2))

    # Confirm (raised, normal, right)
    BRX = DX + DW - px(65) - BW
    neu_raised(img, BRX, BY, BRX+BW, BY+BH,
               r=px(10), light_off=2, light_blur=4, dark_off=7, dark_blur=7)
    icon_check(ImageDraw.Draw(img), BRX + BW//2, BY + BH//2,
               sz=px(8), lw=px(2))

    # ── Save ───────────────────────────────────────────────────────────────────
    rgb = img.convert('RGB')
    rgb.save('/Users/iansmith/mazzy/cmd/slicer/dialog.png', 'PNG')
    print(f"Saved: dialog.png  ({CW}×{CH} px → {CW//2}×{CH//2} logical on 2x display)")


if __name__ == '__main__':
    main()
