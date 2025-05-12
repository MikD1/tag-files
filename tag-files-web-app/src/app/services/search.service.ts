import {inject, Injectable, signal} from '@angular/core';
import {FileType, LibraryApiService, LibraryItemPaginatedList} from './api/library-api.service';
import {Router} from '@angular/router';

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
  readonly searchResults = signal<LibraryItemPaginatedList>(emptyResults);
  private readonly libraryApiService = inject(LibraryApiService);
  private readonly router = inject(Router);

  constructor() {
    this.search();
  }

  search(query?: string, itemType?: FileType) {
    this.libraryApiService.search({
      tagQuery: query,
      itemType: itemType,
      pageIndex: 1,
      pageSize: 100
    }).subscribe((result) => {
      this.searchResults.set(result);
      if (this.router.url !== '/library') {
        this.router.navigate(['/library']);
      }
    })
  }
}
