namespace TagFilesService.Model;

public class PaginatedList<T>(List<T> items, int totalItems, int pageIndex, int totalPages)
{
    public List<T> Items { get; } = items;

    public int TotalItems { get; } = totalItems;

    public int PageIndex { get; } = pageIndex;

    public int TotalPages { get; } = totalPages;
}