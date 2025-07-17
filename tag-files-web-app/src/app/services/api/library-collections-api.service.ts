import {inject, Injectable} from '@angular/core';
import {HttpClient} from '@angular/common/http';
import {Observable} from 'rxjs';
import {LibraryItem} from './library-api.service';

export interface LibraryCollectionDto {
  id: number;
  name: string;
}

export interface CreateLibraryCollectionDto {
  name: string;
}

export interface UpdateLibraryCollectionDto {
  name: string;
}

export interface LibraryCollectionWithItemsDto {
  id: number;
  name: string;
  items: LibraryItem[];
}

@Injectable({
  providedIn: 'root'
})
export class LibraryCollectionsApiService {
  private baseUrl = 'http://localhost:5001/api';
  private readonly http = inject(HttpClient);

  getCollections(): Observable<LibraryCollectionDto[]> {
    return this.http.get<LibraryCollectionDto[]>(`${this.baseUrl}/library-collections`);
  }

  getCollection(id: number): Observable<LibraryCollectionWithItemsDto> {
    return this.http.get<LibraryCollectionWithItemsDto>(`${this.baseUrl}/library-collections/${id}`);
  }

  createCollection(collection: CreateLibraryCollectionDto): Observable<LibraryCollectionDto> {
    return this.http.post<LibraryCollectionDto>(`${this.baseUrl}/library-collections`, collection);
  }

  updateCollection(id: number, collection: UpdateLibraryCollectionDto): Observable<LibraryCollectionDto> {
    return this.http.put<LibraryCollectionDto>(`${this.baseUrl}/library-collections/${id}`, collection);
  }

  deleteCollection(id: number): Observable<void> {
    return this.http.delete<void>(`${this.baseUrl}/library-collections/${id}`);
  }
}
