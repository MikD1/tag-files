import {inject, Injectable} from '@angular/core';
import {HttpClient} from '@angular/common/http';
import {map, Observable} from 'rxjs';
import {convertDuration, LibraryItem} from './library-api.service';

export interface LibraryCollection {
  id: number;
  name: string;
  coverPath?: string | null;
}

export interface CreateLibraryCollectionRequest {
  name: string;
}

export interface UpdateLibraryCollectionRequest {
  id: number;
  name: string;
}

export interface LibraryCollectionWithItems {
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

  getCollections(): Observable<LibraryCollection[]> {
    return this.http.get<LibraryCollection[]>(`${this.baseUrl}/library-collections`);
  }

  getCollection(id: number): Observable<LibraryCollectionWithItems> {
    return this.http.get<LibraryCollectionWithItems>(`${this.baseUrl}/library-collections/${id}`).pipe(
      map(collection => ({
        ...collection,
        items: collection.items.map(item => ({
          ...item,
          videoDuration: item.videoDuration ? convertDuration(item.videoDuration) : undefined
        }))
      }))
    );
  }

  createCollection(collection: CreateLibraryCollectionRequest): Observable<LibraryCollection> {
    return this.http.post<LibraryCollection>(`${this.baseUrl}/library-collections`, collection);
  }

  updateCollection(collection: UpdateLibraryCollectionRequest): Observable<LibraryCollection> {
    return this.http.put<LibraryCollection>(`${this.baseUrl}/library-collections/`, collection);
  }

  deleteCollection(id: number): Observable<void> {
    return this.http.delete<void>(`${this.baseUrl}/library-collections/${id}`);
  }
}
