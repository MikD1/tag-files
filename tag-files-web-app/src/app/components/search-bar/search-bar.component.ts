import {ChangeDetectionStrategy, Component, inject} from '@angular/core';
import {FormControl, ReactiveFormsModule} from '@angular/forms';
import {MatButtonModule} from '@angular/material/button';
import {MatIconModule} from '@angular/material/icon';
import {SearchService} from '../../services/search.service';

@Component({
  selector: 'app-search-bar',
  imports: [MatIconModule, MatButtonModule, ReactiveFormsModule],
  templateUrl: './search-bar.component.html',
  styleUrl: './search-bar.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class SearchBarComponent {
  protected readonly searchQuery = new FormControl<string>('');
  private readonly searchService = inject(SearchService);

  protected search() {
    const searchQuery = this.searchQuery.value ? this.searchQuery.value : '';
    this.searchService.search(searchQuery);
  }
}
