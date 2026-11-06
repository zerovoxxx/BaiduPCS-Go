#!/usr/bin/env python3
"""Generate the embedded runtime JSON; openapi.yaml remains the canonical source."""
import json
from pathlib import Path
import yaml

root=Path(__file__).resolve().parent.parent
spec=yaml.safe_load((root/'openapi.yaml').read_text())
(root/'openapi.json').write_text(json.dumps(spec,ensure_ascii=False,separators=(',',':'))+'\n')
