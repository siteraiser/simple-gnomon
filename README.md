# simple-gnomon-lite

An SQLITE implementation of the GNOMON smart contract indexer for DERO.

Gnomon Api Method Examples:

**GetLastIndexHeight**
```bash
curl -X GET "http://localhost:8080/GetLastIndexHeight" \
```

**GetAllOwnersAndSCIDs**
```bash
curl -X GET "http://localhost:8080/GetAllOwnersAndSCIDs" \
```

**GetAllSCIDVariableDetails**
```bash
curl -X GET "http://localhost:8080/GetAllSCIDVariableDetails?scid=b77b1f5eeff6ed39c8b979c2aeb1c800081fc2ae8f570ad254bedf47bfa977f0" \
```

**GetSCIDVariableDetailsAtTopoheight**
```bash
curl -X GET "http://localhost:8080/GetSCIDVariableDetailsAtTopoheight?scid=805ade9294d01a8c9892c73dc7ddba012eaa0d917348f9b317b706131c82a2d5&height=50000" \
```

**GetSCIDInteractionHeight**
```bash
curl -X GET "http://localhost:8080/GetSCIDInteractionHeight?scid=b77b1f5eeff6ed39c8b979c2aeb1c800081fc2ae8f570ad254bedf47bfa977f0" \
```

**GetSCIDsByClass**
```bash
curl -X GET "http://localhost:8080/GetSCIDsByClass?class=tela" \
```

**GetSCIDsByTags**
```bash
curl -X GET "http://localhost:8080/GetSCIDsByTags?tags=G45-AT&tags=G45-C" \
```
