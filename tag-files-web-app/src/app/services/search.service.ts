import { inject, Injectable, signal } from '@angular/core';
import { LibraryApiService, LibraryItemPaginatedList } from './api/library-api.service';

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
  constructor() {
    this.search('');
  }

  readonly searchResults = signal<LibraryItemPaginatedList>(emptyResults);

  search(query: string) {
    this.libraryApiService.search({ tagQuery: query, pageIndex: 1, pageSize: 100 }).subscribe((result) => { this.searchResults.set(result) })
  }

  private readonly libraryApiService = inject(LibraryApiService);
}
