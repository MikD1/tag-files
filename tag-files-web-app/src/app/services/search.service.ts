import {inject, Injectable, signal} from '@angular/core';
import {LibraryApiService, LibraryItemPaginatedList} from './api/library-api.service';
import {FormControl} from '@angular/forms';
import {FileType} from './api/file-type';
import {SortType} from './api/sort-type';

const emptyResults = {
  items: [],
  totalItems: 0,
  pageIndex: 0,
  totalPages: 0
}

@Injectable({
  providedIn: 'root'
})
export class SearchService {
  readonly searchQuery = new FormControl<string>('');
  readonly isImageSelected = signal<boolean>(false);
  readonly isVideoSelected = signal<boolean>(false);
  readonly searchResults = signal<LibraryItemPaginatedList>(emptyResults);
  private readonly libraryApiService = inject(LibraryApiService);

  constructor() {
    this.search();
  }

  search() {
    let itemType = null
    if (this.isImageSelected() && !this.isVideoSelected()) {
      itemType = FileType.Image;
    }
    if (!this.isImageSelected() && this.isVideoSelected()) {
      itemType = FileType.Video;
    }

    this.libraryApiService.search({
      tagQuery: this.searchQuery.value ? this.searchQuery.value : null,
      itemType: itemType,
      pageIndex: 1,
      pageSize: 300,
      sortBy: SortType.UploadedDesc
    }).subscribe((result) => {
      this.searchResults.set(result);
    })
  }
}
