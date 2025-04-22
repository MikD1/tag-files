import { ChangeDetectionStrategy, Component, computed, inject } from '@angular/core';
import { RouterOutlet } from '@angular/router';
import { AppSidebarComponent } from "./components/sidebar/sidebar.component";
import { AppHeaderComponent } from "./components/header/header.component";
import { MatSidenavModule } from '@angular/material/sidenav';
import { MatToolbarModule } from '@angular/material/toolbar';
import { ImageGridComponent } from "./components/image-grid/image-grid.component";
import { AppStateService } from './services/app-state.service';

@Component({
  selector: 'app-root',
  imports: [RouterOutlet, AppSidebarComponent, AppHeaderComponent, MatSidenavModule, MatToolbarModule, ImageGridComponent],
  templateUrl: './app.component.html',
  styleUrl: './app.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class AppComponent {
  protected readonly isSidebarOpen = computed<boolean>(() => {
    return this.appStateService.getIsSidebarOpen();
  })

  private readonly appStateService = inject(AppStateService);
}
