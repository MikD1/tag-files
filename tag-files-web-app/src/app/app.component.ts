import {
  ChangeDetectionStrategy,
  Component,
  computed,
  inject,
} from "@angular/core";
import { MatSidenavModule } from "@angular/material/sidenav";
import { MatToolbarModule } from "@angular/material/toolbar";
import { RouterOutlet } from "@angular/router";

import { AppHeaderComponent } from "./components/header/header.component";
import { AppSidebarComponent } from "./components/sidebar/sidebar.component";
import { AppStateService } from "./services/app-state.service";

@Component({
  selector: "app-root",
  imports: [
    RouterOutlet,
    AppSidebarComponent,
    AppHeaderComponent,
    MatSidenavModule,
    MatToolbarModule,
  ],
  templateUrl: "./app.component.html",
  styleUrl: "./app.component.scss",
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class AppComponent {
  private readonly appStateService = inject(AppStateService);
  protected readonly isSidebarOpen = computed<boolean>(() => {
    return this.appStateService.getIsSidebarOpen();
  });
}
