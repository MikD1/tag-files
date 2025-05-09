import {inject, Injectable} from '@angular/core';
import {HttpClient} from '@angular/common/http';
import {Observable} from 'rxjs';

export enum FileType {
  Unknown = 'Unknown',
  Text = 'Text',
  Image = 'Image',
  Video = 'Video',
  Audio = 'Audio'
}

export interface SearchRequest {
  tagQuery?: string;
  itemType?: FileType;
  pageIndex: number;
  pageSize: number;
}

export interface AssignTagsRequest {
  itemsList: number[];
  tags: string[];
}

export interface LibraryItem {
  id: number;
  path: string;
  thumbnailPath?: string;
  description?: string;
  uploadedOn: string;
  type: FileType;
  mediaType?: string | null;
  tags: string[];
}

export interface LibraryItemPaginatedList {
  items: LibraryItem[];
  totalItems: number;
  pageIndex: number;
  totalPages: number;
}

@Injectable({
  providedIn: 'root'
})
export class LibraryApiService {
  private baseUrl = 'http://localhost:5001/api';
  private readonly http = inject(HttpClient);

  getItem(id: number): Observable<LibraryItem> {
    return this.http.get<LibraryItem>(`${this.baseUrl}/library/${id}`);
  }

  search(request: SearchRequest): Observable<LibraryItemPaginatedList> {
    return this.http.post<LibraryItemPaginatedList>(`${this.baseUrl}/library/search`, request);
  }

  assignTags(request: AssignTagsRequest): Observable<LibraryItem[]> {
    return this.http.post<LibraryItem[]>(`${this.baseUrl}/library/assign-tags`, request);
  }
}
