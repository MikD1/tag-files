import { Component } from '@angular/core';
import { RouterOutlet } from '@angular/router';
import { AppSidebarComponent } from "./components/sidebar/sidebar.component";
import { AppHeaderComponent } from "./components/header/header.component";
import { MatSidenavModule } from '@angular/material/sidenav';
import { MatToolbarModule } from '@angular/material/toolbar';
import { ImageGridComponent } from "./components/image-grid/image-grid.component";

@Component({
  selector: 'app-root',
  imports: [RouterOutlet, AppSidebarComponent, AppHeaderComponent, MatSidenavModule, MatToolbarModule, ImageGridComponent],
  templateUrl: './app.component.html',
  styleUrl: './app.component.scss'
})
export class AppComponent {
}
