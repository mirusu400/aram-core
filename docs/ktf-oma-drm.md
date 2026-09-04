# KTF WIPI packages wrapped in OMA DRM

Some KTF titles ship their AID-named JAR inside an OMA DRM v2 DCF container
instead of a plain ZIP. `loader/ktf` recognises the container, and opens it
when the content-encryption key is available.

## What the container looks like

The file starts with the eight-byte DCF header `"odcf" 00 02 00 00`, followed
by a single `odrm` container holding `odhe` (headers) and `odda` (data), and a
trailing eight-byte `skip` box.

KTF's `ohdr` uses 16-bit `EncryptionMethod` and `EncryptionPadding` fields
rather than the spec's 8-bit pair, which is what `parseOMADCFHeaders` reads.
Observed values across every sample:

| Field | Value |
|---|---|
| `EncryptionMethod` | `2` (AES-128-CTR) |
| `EncryptionPadding` | `0` |
| `ContentID` | `"00WIPI"` + the eight-digit AID left-padded to 18 characters |
| `RightsIssuerURL` | empty |
| `TextualHeaders` | `"ContentURL:"` |

`odda`'s `OMADRMDataLength` equals `ohdr`'s `PlaintextLength` exactly, so the
object carries **no counter-block prefix** and decryption starts from an
all-zero counter. `openProtectedOMADCF` also accepts the spec's prefixed
layout (ciphertext 16 bytes longer than the plaintext) for containers that do
carry one.

The plaintext is an ordinary JAR, so a decrypted object always begins with a
ZIP local file header. The loader checks that and reports a wrong key rather
than handing garbage to the ZIP reader.

## Where the key comes from

The key is **not** derivable from the package. KTF ran a standard OMA DRM 2.0
rights-issuer at `roi.magicn.com`; the handset's DRM agent
(`DRMAgent2/src/DRMAgent.c`, recoverable from a KTF handset memory image)
acquires a Rights Object per content ID over ROAP and stores it under
`/W/sys/drm/`. Getting the content-encryption key out of that RO needs:

1. the RO file itself, whose container is unlocked with a key derived by
   `PKCS5_PBKDF2_SHA1_DeriveKey` over the agent's `RO_FILE_STRING01` /
   `RO_FILE_STRING02` constants;
2. the handset RSA private key `/W/sys/DEVsk.bin` — itself stored encrypted
   under the agent's `CertEncryptKey00` constant — to run
   `matrixRsaDecryptPrivWithPad` over the RO's `<CipherValue>`;
3. `Enigma_AES_Key_Unwrap` to unwrap the CEK with the recovered KEK.

Because the CEK is chosen per content by the rights issuer and the service is
gone, a DCF package cannot be opened from the file alone. Nothing in the
descriptor (`__adf__`), the icons, or the container carries it: `SLvl`,
`SLvl2` and `PrType` are identical across protected and unprotected titles,
and `ohdr` is exactly full with no room for extended headers.

## Supplying a key

Point `ARAM_KTF_RIGHTS_KEYS` at one or more key files (separated by the
platform list separator). Each line names a content ID and its 16-byte key in
hexadecimal; `#` starts a comment, and the eight-digit AID works as a
shorthand for the full `00WIPI…` identifier:

```text
# 2009 화이트데이
00WIPI000000000001040928 = 000102030405060708090a0b0c0d0e0f
01041FE1 101112131415161718191a1b1c1d1e1f
```

Nothing is read unless the variable is set, so headless runs and corpus
sweeps stay reproducible. Embedders can install keys directly with
`ktf.SetRightsKeys`.
