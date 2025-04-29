import { Component, computed, inject } from '@angular/core';
import lgZoom from 'lightgallery/plugins/zoom';
import lgRotate from 'lightgallery/plugins/rotate';
import { LightgalleryModule } from 'lightgallery/angular';
import { MatTableModule } from '@angular/material/table';
import { MatChipsModule } from '@angular/material/chips';
import { SearchService } from '../../services/search.service';

@Component({
  selector: 'app-image-list',
  imports: [LightgalleryModule, MatTableModule, MatChipsModule],
  templateUrl: './image-list.component.html',
  styleUrl: './image-list.component.scss'
})
export class ImageListComponent {
  protected readonly searchResults = computed(() => { return this.searchService.searchResults() });

  protected gallerySettings = {
    selector: ".gallery-item",
    plugins: [lgZoom, lgRotate],
    download: false,
    counter: false,
    flipHorizontal: false,
    flipVertical: false,
    rotateLeft: false
  };

  protected displayedColumns = ['image', 'tags', 'uploadedOn'];
  private readonly searchService = inject(SearchService);
}
