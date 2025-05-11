import {
  ChangeDetectionStrategy,
  Component,
  computed,
  inject,
} from "@angular/core";
import lgRotate from "lightgallery/plugins/rotate";
import lgVideo from "lightgallery/plugins/video";
import lgZoom from "lightgallery/plugins/zoom";

import { ImageGridComponent } from "../../components/image-grid/image-grid.component";
import { ImageListComponent } from "../../components/image-list/image-list.component";
import {
  AppStateService,
  GalleryViewType,
} from "../../services/app-state.service";
import { SearchService } from "../../services/search.service";

@Component({
  changeDetection: ChangeDetectionStrategy.OnPush,
  selector: "app-library-page",
  imports: [ImageGridComponent, ImageListComponent],
  templateUrl: "./library-page.component.html",
  styleUrl: "./library-page.component.scss",
})
export class LibraryPageComponent {
  protected gallerySettings = {
    plugins: [lgVideo, lgZoom, lgRotate],
    selector: ".gallery-item",
    download: false,
    counter: false,
    flipHorizontal: false,
    flipVertical: false,
    rotateLeft: false,
  };

  protected galleryViewTypes = GalleryViewType;
  private readonly appStateService = inject(AppStateService);
  protected readonly galleryViewType = computed<GalleryViewType>(() => {
    return this.appStateService.getGalleryViewType();
  });
  private readonly searchService = inject(SearchService);
  protected readonly searchResults = computed(() => {
    return this.searchService.searchResults();
  });
}
