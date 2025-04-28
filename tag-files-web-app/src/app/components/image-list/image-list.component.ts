import { Component, computed, inject } from '@angular/core';
import { Observable } from 'rxjs';
import { LibraryItemPaginatedList, LibraryService } from '../../services/library.service';
import lgZoom from 'lightgallery/plugins/zoom';
import lgRotate from 'lightgallery/plugins/rotate';
import { AsyncPipe } from '@angular/common';
import { LightgalleryModule } from 'lightgallery/angular';
import { MatTableModule } from '@angular/material/table';
import { MatChipsModule } from '@angular/material/chips';

@Component({
  selector: 'app-image-list',
  imports: [AsyncPipe, LightgalleryModule, MatTableModule, MatChipsModule],
  templateUrl: './image-list.component.html',
  styleUrl: './image-list.component.scss'
})
export class ImageListComponent {
  constructor() {
    this.libraryItemsList = this.libraryService.search({ tagQuery: "", pageIndex: 1, pageSize: 100 })
  }

  protected gallerySettings = {
    selector: ".gallery-item",
    plugins: [lgZoom, lgRotate],
    download: false,
    counter: false,
    flipHorizontal: false,
    flipVertical: false,
    rotateLeft: false
  };

  protected libraryItemsList: Observable<LibraryItemPaginatedList>;
  protected displayedColumns = ['image', 'tags', 'uploadedOn'];

  private readonly libraryService = inject(LibraryService);
}
