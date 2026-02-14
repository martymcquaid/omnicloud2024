# DCP Archive Investigation: `/APPBOX_DATA/storage/DCP/Archive/`

**Date:** 2025-02-13

---

## 1. Overview

| Metric | Value |
|--------|--------|
| **Total size** | **22 TB** |
| **Top-level items** | 204 (directories) |
| **Total files** | **4,382** |
| **DCP packages** (folders with ASSETMAP) | **~307** |
| **MXF asset files** | **2,501** |
| **XML metadata files** | **1,219** |

---

## 2. How DCP Packages Are Made Up

Each **DCP (Digital Cinema Package)** is a folder that follows **SMPTE DCP** standards. A valid package contains:

### 2.1 Required metadata (root of package)

| File | Purpose | Standard |
|------|---------|----------|
| **ASSETMAP.xml** | Master index: lists every asset in the DCP by UUID, path, and byte range (chunk). Entry point for ingest/validation. | SMPTE 429-9 |
| **VOLINDEX** (or VOLINDEX.xml) | Volume index; usually `Index=1` for single-volume. | SMPTE 429-9 |
| **PKL_&lt;uuid&gt;.xml** | Packing List: assets with Id, Hash (base64), Size, Type. Used for integrity checking. | SMPTE 429-8 |
| **CPL_&lt;uuid&gt;.xml** | Composition Playlist: reels, picture/sound/subtitle references, edit rate, duration, aspect ratio, content kind (e.g. feature), ratings, territory. | SMPTE 429-7 |

### 2.2 Asset files (MXF)

| Suffix / role | Content |
|----------------|--------|
| **\*_v.mxf** | Picture (JPEG 2000 in MXF). Reels named e.g. `R01_<uuid>_SMPTE_v.mxf`. |
| **\*_snd.mxf** | Sound (e.g. 5.1, 7.1, 8ch). Multiple tracks/reels. |
| **\*_tt.mxf** | Timed text / subtitles (e.g. `_en-sub_en_`). |

- **Picture**: Stored resolution in CPL (e.g. `4096 1716`), edit rate (e.g. `24 1`), duration per reel.
- **Sound**: Channel config in CPL (e.g. 51 = 5.1, 71 = 7.1).
- **Subtitles**: Language, duration, entry point in CPL.

### 2.3 Other files sometimes present

- **.ttf** – Fonts (82 in archive).
- **.xml** – Extra CPL/PKL or metadata (1,219 total).
- **.nfo**, **.partial**, **.aspera-ckpt** – Transfer/ops artefacts, not part of core DCP spec.

---

## 3. Directory Layout in Your Archive

Two patterns:

1. **Single package per folder**  
   One folder = one DCP. Name is usually a long technical string, e.g.  
   `1023182_TheBatman_OV-ENGLISH-OCAP_2D-4K_51-71-VI_SMPTE_ASSCLN-2cf368a9-25e5-4198-b7bd-1325ac0c9f4a`

2. **Title folder containing multiple packages**  
   Human-readable title as parent, DCPs as subfolders, e.g.  
   - `10 Things I Hate About You/`  
     - `10ThingsIHateA_FTR_F_EN-XX_US-13_51_2K_DI_20190117_DSS_IOP_OV`  
     - `10ThingsIHateA_RTG-F_F_EN-XX_IE-12A_51_2K_IND_20240126_DLX_IOP_OV`  
     - `10ThingsIHateA_RTG-F_F_EN-XX_UK-12A_MOS_2K_DI_20240125_DLX_IOP_OV`  
   - `Shrek/`, `Trainspotting/`, `Jurassic Park/`, etc.

Naming often encodes: **Title**, **FTR/TLR/RTG** (feature/trailer/rating), **territory** (UK, IE, US), **language**, **resolution** (2K/4K), **sound** (51/71), **date**, **facility**, **OV/VF**, and a **UUID**.

---

## 4. Example: One Package in Detail (The Batman)

**Path:**  
`1023182_TheBatman_OV-ENGLISH-OCAP_2D-4K_51-71-VI_SMPTE_ASSCLN-2cf368a9-25e5-4198-b7bd-1325ac0c9f4a/`

| Attribute | Value |
|-----------|--------|
| **Package size** | ~340 GB |
| **ASSETMAP Id** | `urn:uuid:2cf368a9-25e5-4198-b7bd-1325ac0c9f4a` |
| **Creator** | Cipher AssetMapGen 1.1.0 |
| **Issuer** | DLX |
| **Content** | Feature (FTR), 2D, 4K, 5.1 & 7.1 (VI), OCAP subtitles |

**Contents (high level):**

- **ASSETMAP.xml** – ~23 KB; lists all assets and paths.
- **VOLINDEX.xml** – single volume.
- **Multiple PKL_*.xml** – packing lists for different compositions.
- **Multiple CPL_*.xml** – playlists (e.g. UK-IE 51-VI 4K version).
- **Picture MXF** – 9 reels (`R01_…_SMPTE_v.mxf` … `R09_…`), each tens of GB.
- **Sound MXF** – OV 51 8-track, VI 51 8-track, VI 71 16-track, plus DC brand piece; reels R001–R009 (and R010 for brand).
- **Subtitle MXF** – `739125_TheBatman_smpte_enc_en-sub_en_R00x_tt.mxf` (9 reels).

**CPL metadata (from one CPL):**

- **ContentKind:** feature  
- **EditRate:** 24 1  
- **ScreenAspectRatio:** 4096 1716  
- **ContentTitleText:** e.g. `TheBatman_FTR-3_S_EN-EN-OCAP_UK-IE_51-VI_4K_WR_20220302_DLX_SMPTE_VF`  
- **FullContentTitleText:** "The Batman (2022)"  
- **MainSoundConfiguration:** 51/L,R,C,LFE,Ls,Rs,-,VIN  
- **MainPictureStoredArea:** 4096×1716  

---

## 5. File Type Summary (Archive-Wide)

| Type | Count | Notes |
|------|--------|------|
| **.mxf** | 2,501 | Picture, sound, subtitle assets (bulk of 22 TB) |
| **.xml** | 1,219 | ASSETMAP, VOLINDEX, CPL, PKL, other metadata |
| **.ttf** | 82 | Fonts (e.g. for subtitles) |
| **.snd** | 39 | Likely filename fragment (e.g. `*_snd.mxf`) |
| **.pic** | 35 | Likely filename fragment |
| **.nfo** | 16 | Info/transfer metadata |
| **.partial** / **.aspera-ckpt** | 11 each | Transfer/checkpoint files |
| **.cpl** / **.pkl** / **.am** | 9 / 8 / 6 | Extension-only references to CPL/PKL/ASSETMAP |
| **.sub** / **.fnt** | 5 each | Subtitle/font related |
| **.tmp** | 2 | Temporary |
| **.txt** / **.iso** | 1 each | Misc |

(Some “extensions” in the scan are path fragments, not real file types.)

---

## 6. Attributes You Can Rely On

- **Identity:** ASSETMAP `Id` (UUID) and CPL/PKL `Id` (UUID) uniquely identify the package and each composition.
- **Integrity:** PKL `Hash` (base64) and `Size` per asset; ASSETMAP `Length` per chunk.
- **Playback:** CPL defines reels, edit rate, duration, entry point, picture size, sound config, subtitle language.
- **Delivery metadata:** Issuer, Creator, IssueDate, AnnotationText, ContentTitleText, territory, rating, facility (e.g. DLX, MPS).

---

## 7. Summary

- **22 TB** of DCP content in **/APPBOX_DATA/storage/DCP/Archive/**.
- **~307** distinct DCP packages; **4,382** files; **2,501** MXF assets.
- Each package is **SMPTE DCP**: ASSETMAP + VOLINDEX + PKL + CPL + MXF (picture, sound, subtitles).
- Folder layout mixes **single-package folders** (long technical names) and **title folders** with multiple packages (FTR/RTG, territories, resolutions).
- One large 4K feature (The Batman) example is **~340 GB**; total size is dominated by **MXF** (picture and sound).

If you want, next steps can be: (1) a script to list all packages with ASSETMAP Id and size, or (2) parsing CPL/ASSETMAP to build a DB table (e.g. in OmniCloud) of packages and their attributes.
