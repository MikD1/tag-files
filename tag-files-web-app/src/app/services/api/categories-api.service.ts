import {inject, Injectable} from '@angular/core';
import {HttpClient} from '@angular/common/http';
import {Observable} from 'rxjs';
import {FileType} from './file-type';

export interface Category {
  id: number;
  name: string;
  tagQuery: string | null;
  itemsType: FileType;
}

export interface CreateCategory {
  name: string;
  tagQuery: string | null;
  itemsType: FileType | null;
}

export interface UpdateCategory {
  name: string;
  tagQuery: string | null;
  itemsType: FileType | null;
}

@Injectable({
  providedIn: 'root'
})
export class CategoriesApiService {
  private baseUrl = 'http://localhost:5001/api';
  private readonly http = inject(HttpClient);

  getCategories(): Observable<Category[]> {
    return this.http.get<Category[]>(`${this.baseUrl}/categories`);
  }

  getCategory(id: number): Observable<Category> {
    return this.http.get<Category>(`${this.baseUrl}/categories/${id}`);
  }

  createCategory(category: CreateCategory): Observable<Category> {
    return this.http.post<Category>(`${this.baseUrl}/categories`, category);
  }

  updateCategory(id: number, category: UpdateCategory): Observable<Category> {
    return this.http.put<Category>(`${this.baseUrl}/categories/${id}`, category);
  }

  deleteCategory(id: number): Observable<void> {
    return this.http.delete<void>(`${this.baseUrl}/categories/${id}`);
  }
}
