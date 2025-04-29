import { ChangeDetectionStrategy, Component, model, output, signal } from '@angular/core';
import { FormControl, ReactiveFormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatIconModule } from '@angular/material/icon';

@Component({
  selector: 'app-search-bar',
  imports: [MatIconModule, MatButtonModule, ReactiveFormsModule],
  templateUrl: './search-bar.component.html',
  styleUrl: './search-bar.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class SearchBarComponent {
  searchClicked = output<string>();

  protected onSearchClick() {
    if (this.searchQuery.value) {
      this.searchClicked.emit(this.searchQuery.value);
    }
  }

  protected readonly searchQuery = new FormControl<string>('');
}