import {ChangeDetectionStrategy, Component, computed, inject, signal} from '@angular/core';
import {MatToolbarModule} from '@angular/material/toolbar';
import {MatIconModule} from '@angular/material/icon';
import {MatButtonModule} from '@angular/material/button';
import {AppStateService, GalleryViewType} from '../../services/app-state.service';
import {SearchBarComponent} from '../search-bar/search-bar.component';
import {MatButtonToggleModule} from '@angular/material/button-toggle';
import {NavigationEnd, Router} from '@angular/router';
import {filter} from 'rxjs';

@Component({
  selector: 'app-header',
  imports: [MatToolbarModule, MatIconModule, MatButtonModule, SearchBarComponent, MatButtonToggleModule],
  templateUrl: './header.component.html',
  styleUrl: './header.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class AppHeaderComponent {
  protected galleryViewTypes = GalleryViewType;
  protected readonly appStateService = inject(AppStateService);
  protected readonly isLibraryPage = signal<boolean>(false);
  protected readonly galleryViewType = computed<GalleryViewType>(() => {
    return this.appStateService.getGalleryViewType();
  })
  private readonly router = inject(Router);

  constructor() {
    this.router.events.pipe(filter(event => event instanceof NavigationEnd)).subscribe(() => {
      this.isLibraryPage.set(this.router.url === '/library');
    });
  }
}
