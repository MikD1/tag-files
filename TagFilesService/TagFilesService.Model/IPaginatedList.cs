namespace TagFilesService.Model;

public interface IPaginatedList<T>
{
    List<T> Items { get; }

    int TotalItems { get; }

    int PageIndex { get; }

    int TotalPages { get; }
}