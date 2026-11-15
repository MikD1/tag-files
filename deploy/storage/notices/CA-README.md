# ca-certificates

Mozilla CA certificate store packaged for Alpine Linux.

Downloads `certdata.txt` from Mozilla NSS, splits it into individual `.crt`
files, and provides `update-ca-certificates` and `c_rehash` utilities.

## Updating

1. Update `NSS_VERSION` in `Makefile` to the desired NSS tag or run
   `scripts/update-version.sh --latest`
2. Run `make update` to fetch the latest `certdata.txt`.
3. Run `make` to regenerate all `.crt` files and `cert.pem`.
4. Run `make update-version` to set `VERSION` from the tag's commit date.
5. Commit.
