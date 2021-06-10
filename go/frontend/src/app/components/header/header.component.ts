import {Component, EventEmitter, Input, OnInit, Output} from '@angular/core';
import { ClarityIcons, cloudIcon } from '@cds/core/icon';
import '@cds/core/icon/register.js';
import {AnalyzerRunService} from '../../services/analyzerrun.service';
import { pushErrorNotification } from '../../utils/notificationutil';
import {ToastrService} from "ngx-toastr";

@Component({
  selector: 'app-header',
  templateUrl: './header.component.html',
  styleUrls: ['./header.component.css']
})
export class HeaderComponent implements OnInit {

  @Input()
  selectedRunId: number | undefined;

  @Output()
  selectedRunIdChange = new EventEmitter();

  forgeVersion: string = 'v3.2.5-rc1';

  constructor(private analyzerRunService: AnalyzerRunService, public toastr: ToastrService) {
    const csaSvg = `<?xml version="1.0" encoding="UTF-8" standalone="no"?>
<!DOCTYPE svg PUBLIC "-//W3C//DTD SVG 1.1//EN" "http://www.w3.org/Graphics/SVG/1.1/DTD/svg11.dtd">
<svg version="1.1" id="Layer_1" xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" x="0px" y="0px" width="278px" height="222px" viewBox="0 0 278 222" enable-background="new 0 0 278 222" xml:space="preserve">  <image id="image0" width="278" height="222" x="0" y="0"
    href="data:image/png;base64,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
iFVTuWHq+FJiKeWacbPlKvv2fa5SlkyHrYl9lGZKwiGjadMMOt60KjzX8xkAAAAAAAAAAAAAAAAA
AAAAAAAAAAAAAAAAAAAAAAAAAFA3/gch/uNVRd6xFAAAAIplWElmTU0AKgAAAAgABAEaAAUAAAAB
AAAAPgEbAAUAAAABAAAARgEoAAMAAAABAAIAAIdpAAQAAAABAAAATgAAAAAAAACQAAAAAQAAAJAA
AAABAAOShgAHAAAAEgAAAHigAgAEAAAAAQAAARagAwAEAAAAAQAAAN4AAAAAQVNDSUkAAABTY3Jl
ZW5zaG90KWyuXwAAACV0RVh0ZGF0ZTpjcmVhdGUAMjAyMS0wNS0yOFQwMjoyMTo0MiswMDowMEY2
+GcAAAAldEVYdGRhdGU6bW9kaWZ5ADIwMjEtMDUtMjhUMDI6MjE6NDIrMDA6MDA3a0DbAAAAEnRF
WHRleGlmOkV4aWZPZmZzZXQANzjJ1HsnAAAAGHRFWHRleGlmOlBpeGVsWERpbWVuc2lvbgAyNzgF
7uDBAAAAGHRFWHRleGlmOlBpeGVsWURpbWVuc2lvbgAyMjIFQxzsAAAAXHRFWHRleGlmOlVzZXJD
b21tZW50ADY1LCA4MywgNjcsIDczLCA3MywgMCwgMCwgMCwgODMsIDk5LCAxMTQsIDEwMSwgMTAx
LCAxMTAsIDExNSwgMTA0LCAxMTEsIDExNkC4H3IAAAAodEVYdGljYzpjb3B5cmlnaHQAQ29weXJp
Z2h0IEFwcGxlIEluYy4sIDIwMjF9ve4mAAAAF3RFWHRpY2M6ZGVzY3JpcHRpb24ARGlzcGxheRcb
lbgAAAAASUVORK5CYII=" />
</svg>`;
    ClarityIcons.addIcons( ['csa', csaSvg]);
    ClarityIcons.addIcons(cloudIcon);
  }

  ngOnInit(): void {
    this.analyzerRunService.getForgeVersion().subscribe(version => {
      if (version && version.length > 0) {
        this.forgeVersion = version;
      }
    }, error => {
      pushErrorNotification(error, this.toastr);
    });
  }

}
